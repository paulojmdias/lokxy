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
	"github.com/grafana/loki/v3/pkg/logqlmodel/stats"

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
	var mergedStreams []json.RawMessage
	var mergedMatrix []json.RawMessage
	var mergedVector []json.RawMessage
	var scalarResult json.RawMessage
	var hasScalarResult bool
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

		// Parse only top-level query response and keep result payload raw.
		// This avoids strict stream entry decoding issues with newer Loki formats.
		var queryResult lokiQueryResponse
		if err := json.Unmarshal(bodyBytes, &queryResult); err != nil {
			level.Error(logger).Log("msg", "Failed to unmarshal backend query response", "err", err)
			continue
		}

		level.Debug(logger).Log("msg", "Raw JSON body", "rawBody", string(bodyBytes))

		// Check if encodingFlags is present in the response and extract it
		for _, flag := range queryResult.Data.EncodingFlags {
			encodingFlagsMap[flag] = struct{}{}
		}

		if !hasResultType {
			resultType = queryResult.Data.ResultType
			hasResultType = true
		}

		if queryResult.Data.ResultType != resultType {
			level.Warn(logger).Log(
				"msg", "Skipping response with mismatched result type",
				"backend", backendResp.BackendName,
				"expected", resultType,
				"got", queryResult.Data.ResultType,
			)
			continue
		}

		// Process based on ResultType
		switch queryResult.Data.ResultType {
		case loghttp.ResultTypeStream:
			streams, err := decodeResultArray(queryResult.Data.Result)
			if err != nil {
				level.Error(logger).Log("msg", "Failed to decode streams result", "err", err)
				continue
			}
			mergedStreams = append(mergedStreams, streams...)

		case loghttp.ResultTypeMatrix:
			matrix, err := decodeResultArray(queryResult.Data.Result)
			if err != nil {
				level.Error(logger).Log("msg", "Failed to decode matrix result", "err", err)
				continue
			}
			mergedMatrix = append(mergedMatrix, matrix...)

		case loghttp.ResultTypeVector:
			vector, err := decodeResultArray(queryResult.Data.Result)
			if err != nil {
				level.Error(logger).Log("msg", "Failed to decode vector result", "err", err)
				continue
			}
			mergedVector = append(mergedVector, vector...)

		case loghttp.ResultTypeScalar:
			result := bytes.TrimSpace(queryResult.Data.Result)
			if len(result) > 0 && !bytes.Equal(result, []byte("null")) {
				scalarResult = result
				hasScalarResult = true
			}

		default:
			level.Warn(logger).Log(
				"msg", "Unknown result type from backend response",
				"backend", backendResp.BackendName,
				"resultType", queryResult.Data.ResultType,
			)
			continue
		}

		// Merge statistics
		mergedStats.Merge(queryResult.Data.Statistics)
		validResponses++
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
		if len(mergedStreams) == 0 {
			finalResult = []json.RawMessage{}
		} else {
			finalResult = mergedStreams
		}

	case loghttp.ResultTypeMatrix:
		if len(mergedMatrix) == 0 {
			finalResult = []json.RawMessage{}
		} else {
			finalResult = mergedMatrix
		}

	case loghttp.ResultTypeVector:
		if len(mergedVector) == 0 {
			finalResult = []json.RawMessage{}
		} else {
			finalResult = mergedVector
		}

	case loghttp.ResultTypeScalar:
		if hasScalarResult {
			finalResult = scalarResult
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
