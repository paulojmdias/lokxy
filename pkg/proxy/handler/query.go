package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/grafana/loki/v3/pkg/loghttp"
	"github.com/grafana/loki/v3/pkg/logqlmodel/stats" // For statistics
	"github.com/prometheus/prometheus/model/labels"

	"github.com/paulojmdias/lokxy/pkg/proxy/proxyresponse"
)

type lokiQueryFlags struct {
	Data struct {
		EncodingFlags []string `json:"encodingFlags,omitempty"`
	} `json:"data"`
}

type lokiQueryEnvelope struct {
	Data struct {
		ResultType loghttp.ResultType `json:"resultType"`
		Result     json.RawMessage    `json:"result"`
		Statistics stats.Result       `json:"stats"`
	} `json:"data"`
}

// Handle Loki query and query_range responses
func HandleLokiQueries(_ context.Context, w http.ResponseWriter, results <-chan *proxyresponse.BackendResponse, logger log.Logger) {
	var mergedStreams loghttp.Streams
	var mergedMatrix loghttp.Matrix
	var mergedVector loghttp.Vector
	var scalarResult loghttp.Scalar
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

		// Extract encodingFlags (loghttp.QueryResponse doesn't expose it).
		var flags lokiQueryFlags
		if err := json.Unmarshal(bodyBytes, &flags); err == nil {
			for _, f := range flags.Data.EncodingFlags {
				encodingFlagsMap[f] = struct{}{}
			}
		}

		// Decode using Loki library types.
		var queryResult loghttp.QueryResponse
		if err := json.Unmarshal(bodyBytes, &queryResult); err != nil {
			// Loki library doesn't accept `"result": null` for array-shaped results, but upstreams may emit it.
			// Normalize it into an empty array for the corresponding result type.
			var env lokiQueryEnvelope
			if envErr := json.Unmarshal(bodyBytes, &env); envErr == nil && bytes.Equal(bytes.TrimSpace(env.Data.Result), []byte("null")) {
				queryResult.Data.ResultType = env.Data.ResultType
				queryResult.Data.Statistics = env.Data.Statistics
				switch env.Data.ResultType {
				case loghttp.ResultTypeStream:
					queryResult.Data.Result = loghttp.Streams{}
				case loghttp.ResultTypeMatrix:
					queryResult.Data.Result = loghttp.Matrix{}
				case loghttp.ResultTypeVector:
					queryResult.Data.Result = loghttp.Vector{}
				case loghttp.ResultTypeScalar:
					// scalar null -> skip (no valid result)
					level.Warn(logger).Log("msg", "Skipping scalar null result", "backend", backendResp.BackendName)
					continue
				default:
					level.Warn(logger).Log("msg", "Skipping response with unknown result type", "backend", backendResp.BackendName, "resultType", env.Data.ResultType)
					continue
				}
			} else {
				level.Error(logger).Log("msg", "Failed to unmarshal into loghttp.QueryResponse", "err", err)
				continue
			}
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

		switch queryResult.Data.ResultType {
		case loghttp.ResultTypeStream:
			streams, ok := queryResult.Data.Result.(loghttp.Streams)
			if !ok {
				level.Error(logger).Log("msg", "Failed to assert type to loghttp.Streams", "backend", backendResp.BackendName)
				continue
			}
			mergedStreams = append(mergedStreams, streams...)

		case loghttp.ResultTypeMatrix:
			matrix, ok := queryResult.Data.Result.(loghttp.Matrix)
			if !ok {
				level.Error(logger).Log("msg", "Failed to assert type to loghttp.Matrix", "backend", backendResp.BackendName)
				continue
			}
			mergedMatrix = append(mergedMatrix, matrix...)

		case loghttp.ResultTypeVector:
			vector, ok := queryResult.Data.Result.(loghttp.Vector)
			if !ok {
				level.Error(logger).Log("msg", "Failed to assert type to loghttp.Vector", "backend", backendResp.BackendName)
				continue
			}
			mergedVector = append(mergedVector, vector...)

		case loghttp.ResultTypeScalar:
			scalar, ok := queryResult.Data.Result.(loghttp.Scalar)
			if !ok {
				level.Error(logger).Log("msg", "Failed to assert type to loghttp.Scalar", "backend", backendResp.BackendName)
				continue
			}
			scalarResult = scalar
			hasScalarResult = true

		default:
			level.Warn(logger).Log(
				"msg", "Unknown result type from backend response",
				"backend", backendResp.BackendName,
				"resultType", queryResult.Data.ResultType,
			)
			continue
		}

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
		formattedResults := make([]map[string]any, 0, len(mergedStreams))
		for _, stream := range mergedStreams {
			values := make([][]any, 0, len(stream.Entries))
			for _, entry := range stream.Entries {
				v := []any{
					strconv.FormatInt(entry.Timestamp.UnixNano(), 10),
					entry.Line,
				}

				metaObj := make(map[string]any)
				if entry.StructuredMetadata.Len() > 0 {
					metaObj["structuredMetadata"] = labelsToMap(entry.StructuredMetadata)
				}
				if entry.Parsed.Len() > 0 {
					metaObj["parsed"] = labelsToMap(entry.Parsed)
				}
				if len(metaObj) > 0 {
					v = append(v, metaObj)
				}

				values = append(values, v)
			}

			formattedResults = append(formattedResults, map[string]any{
				"stream": stream.Labels,
				"values": values,
			})
		}
		finalResult = formattedResults

	case loghttp.ResultTypeMatrix:
		formattedMatrix := make([]map[string]any, 0, len(mergedMatrix))
		for _, matrixEntry := range mergedMatrix {
			values := make([][]any, 0, len(matrixEntry.Values))
			for _, value := range matrixEntry.Values {
				values = append(values, []any{
					value.Timestamp.Unix(),
					value.Value,
				})
			}
			formattedMatrix = append(formattedMatrix, map[string]any{
				"metric": matrixEntry.Metric,
				"values": values,
			})
		}
		finalResult = formattedMatrix

	case loghttp.ResultTypeVector:
		formattedVector := make([]map[string]any, 0, len(mergedVector))
		for _, vectorEntry := range mergedVector {
			formattedVector = append(formattedVector, map[string]any{
				"metric": vectorEntry.Metric,
				"value": []any{
					vectorEntry.Timestamp,
					vectorEntry.Value,
				},
			})
		}
		finalResult = formattedVector

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

func labelsToMap(ls labels.Labels) map[string]string {
	out := make(map[string]string, ls.Len())
	ls.Range(func(l labels.Label) {
		out[l.Name] = l.Value
	})
	return out
}
