package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/grafana/loki/v3/pkg/loghttp"
	"github.com/grafana/loki/v3/pkg/logqlmodel/stats" // For statistics

	"github.com/paulojmdias/lokxy/pkg/proxy/proxyresponse"
)

type lokiQueryResponse struct {
	Data struct {
		ResultType    loghttp.ResultType `json:"resultType"`
		Result        json.RawMessage    `json:"result"`
		Statistics    stats.Result       `json:"stats"`
		EncodingFlags []string           `json:"encodingFlags,omitempty"`
	} `json:"data"`
}

// Handle Loki query and query_range responses
func HandleLokiQueries(_ context.Context, w http.ResponseWriter, results <-chan *proxyresponse.BackendResponse, logger log.Logger) {
	// Merge result payload as opaque JSON elements. Loki's streams entry format
	// evolves (and can include categorized-labels encodings) that strict structs
	// may reject even when the JSON is valid.
	var mergedStreamsRaw []json.RawMessage
	var mergedMatrixRaw []json.RawMessage
	var mergedVectorRaw []json.RawMessage
	var scalarResultRaw json.RawMessage
	var hasScalarResultRaw bool

	var resultType loghttp.ResultType
	var hasResultType bool
	var validResponses int
	var mergedStats stats.Result
	encodingFlagsMap := make(map[string]struct{})

	for backendResp := range results {
		resp := backendResp.Response
		// Read the entire body
		bodyBytes, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			logFn := level.Error(logger)
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				logFn = level.Warn(logger)
			}
			_ = logFn.Log("msg", "Failed to read response body",
				"backend", backendResp.BackendName,
				"err", err,
			)
			continue
		}

		// Log the full body for debugging
		level.Debug(logger).Log("msg", "Complete body received", "body", string(bodyBytes))

		var top lokiQueryResponse
		if err := json.Unmarshal(bodyBytes, &top); err != nil {
			level.Error(logger).Log("msg", "Failed to unmarshal backend query response", "err", err)
			continue
		}

		// Collect encodingFlags from response.
		for _, flag := range top.Data.EncodingFlags {
			encodingFlagsMap[flag] = struct{}{}
		}

		if !hasResultType {
			resultType = top.Data.ResultType
			hasResultType = true
		}

		if top.Data.ResultType != resultType {
			level.Warn(logger).Log(
				"msg", "Skipping response with mismatched result type",
				"backend", backendResp.BackendName,
				"expected", resultType,
				"got", top.Data.ResultType,
			)
			continue
		}

		accepted := true
		switch top.Data.ResultType {
		case loghttp.ResultTypeStream:
			streamsRaw, err := decodeResultArray(top.Data.Result)
			if err != nil {
				level.Error(logger).Log("msg", "Failed to decode raw streams result", "err", err)
				continue
			}
			mergedStreamsRaw = append(mergedStreamsRaw, streamsRaw...)
		case loghttp.ResultTypeMatrix:
			matrixRaw, err := decodeResultArray(top.Data.Result)
			if err != nil {
				level.Error(logger).Log("msg", "Failed to decode raw matrix result", "err", err)
				continue
			}
			mergedMatrixRaw = append(mergedMatrixRaw, matrixRaw...)
		case loghttp.ResultTypeVector:
			vectorRaw, err := decodeResultArray(top.Data.Result)
			if err != nil {
				level.Error(logger).Log("msg", "Failed to decode raw vector result", "err", err)
				continue
			}
			mergedVectorRaw = append(mergedVectorRaw, vectorRaw...)
		case loghttp.ResultTypeScalar:
			raw := bytes.TrimSpace(top.Data.Result)
			if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
				level.Warn(logger).Log("msg", "Skipping scalar null result", "backend", backendResp.BackendName)
				accepted = false
			} else if !hasScalarResultRaw {
				scalarResultRaw = raw
				hasScalarResultRaw = true
			} else if !bytes.Equal(bytes.TrimSpace(scalarResultRaw), raw) {
				level.Warn(logger).Log("msg", "Ignoring mismatched scalar result from backend", "backend", backendResp.BackendName)
				accepted = false
			}
		default:
			level.Warn(logger).Log("msg", "Unknown result type from backend response", "backend", backendResp.BackendName, "resultType", top.Data.ResultType)
			continue
		}

		if accepted {
			mergedStats.Merge(top.Data.Statistics)
			validResponses++
		}
	}

	if validResponses == 0 || !hasResultType {
		level.Error(logger).Log("msg", "No valid query responses from upstreams")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		if err := json.NewEncoder(w).Encode(map[string]any{
			"status":    "error",
			"errorType": "backend_error",
			"error":     "No valid query responses from upstreams",
		}); err != nil {
			level.Error(logger).Log("msg", "Failed to encode error response", "err", err)
		}
		return
	}

	// Prepare final response
	var finalResult any = []any{}

	switch resultType {
	case loghttp.ResultTypeStream:
		if len(mergedStreamsRaw) == 0 {
			finalResult = []json.RawMessage{}
		} else {
			finalResult = mergedStreamsRaw
		}

	case loghttp.ResultTypeMatrix:
		if len(mergedMatrixRaw) == 0 {
			finalResult = []json.RawMessage{}
		} else {
			finalResult = mergedMatrixRaw
		}

	case loghttp.ResultTypeVector:
		if len(mergedVectorRaw) == 0 {
			finalResult = []json.RawMessage{}
		} else {
			finalResult = mergedVectorRaw
		}

	case loghttp.ResultTypeScalar:
		if hasScalarResultRaw {
			finalResult = scalarResultRaw
		}
	}

	finalResponse := map[string]any{
		"status": "success",
		"data": map[string]any{
			"resultType": resultType,
			"result":     finalResult,
			"stats":      mergedStats,
		},
	}

	// Convert map back to a slice of strings
	var encodingFlags []string
	for flag := range encodingFlagsMap {
		encodingFlags = append(encodingFlags, flag)
	}
	sort.Strings(encodingFlags)

	// Only add encodingFlags if it's defined in any of the responses
	if len(encodingFlags) > 0 {
		finalResponse["data"].(map[string]any)["encodingFlags"] = encodingFlags
	}

	if err := json.NewEncoder(w).Encode(finalResponse); err != nil {
		level.Error(logger).Log("msg", "Failed to encode final response", "err", err)
	}
}

func decodeResultArray(raw json.RawMessage) ([]json.RawMessage, error) {
	result := bytes.TrimSpace(raw)
	if len(result) == 0 || bytes.Equal(result, []byte("null")) {
		return nil, nil
	}

	var decoded []json.RawMessage
	if err := json.Unmarshal(result, &decoded); err != nil {
		return nil, err
	}

	return decoded, nil
}
