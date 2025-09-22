package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/grafana/loki/v3/pkg/loghttp"
	"github.com/grafana/loki/v3/pkg/logqlmodel/stats"
)

func HandleLokiQueries(w http.ResponseWriter, results <-chan *http.Response, logger log.Logger) {
	var mergedStreams []loghttp.Stream
	var mergedMatrix loghttp.Matrix
	var mergedVector loghttp.Vector
	var resultType loghttp.ResultType
	var mergedStats stats.Result
	var encodingFlagsMap = make(map[string]struct{})

	for resp := range results {
		defer resp.Body.Close()

		// Forward upstream error responses directly
		if resp.StatusCode >= 400 {
			bodyBytes, _ := io.ReadAll(resp.Body)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(resp.StatusCode)
			_, _ = w.Write(bodyBytes)
			return
		}

		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			level.Error(logger).Log("msg", "Failed to read response body", "err", err)
			continue
		}

		var rawBody map[string]any
		if json.Valid(bodyBytes) {
			_ = json.Unmarshal(bodyBytes, &rawBody)
		}

		if data, ok := rawBody["data"].(map[string]any); ok {
			if flags, ok := data["encodingFlags"].([]any); ok {
				for _, flag := range flags {
					if flagStr, ok := flag.(string); ok {
						encodingFlagsMap[flagStr] = struct{}{}
					}
				}
			}
		}

		var queryResult loghttp.QueryResponse
		if err := json.Unmarshal(bodyBytes, &queryResult); err != nil {
			level.Error(logger).Log("msg", "Failed to unmarshal into loghttp.QueryResponse", "err", err)
			continue
		}

		resultType = queryResult.Data.ResultType

		switch queryResult.Data.ResultType {
		case loghttp.ResultTypeStream:
			if streams, ok := queryResult.Data.Result.(loghttp.Streams); ok {
				mergedStreams = append(mergedStreams, streams...)
			}
		case loghttp.ResultTypeMatrix:
			if matrix, ok := queryResult.Data.Result.(loghttp.Matrix); ok {
				mergedMatrix = append(mergedMatrix, matrix...)
			}
		case loghttp.ResultTypeVector:
			if vector, ok := queryResult.Data.Result.(loghttp.Vector); ok {
				mergedVector = append(mergedVector, vector...)
			}
		}

		mergedStats.Merge(queryResult.Data.Statistics)
	}

	var finalResult any = []any{}
	switch resultType {
	case loghttp.ResultTypeStream:
		var formattedResults []map[string]any
		for _, stream := range mergedStreams {
			values := make([][]any, len(stream.Entries))
			for i, entry := range stream.Entries {
				values[i] = []any{
					strconv.FormatInt(entry.Timestamp.UnixNano(), 10),
					entry.Line,
				}
				metadata := make(map[string]any)
				if len(entry.StructuredMetadata) > 0 {
					metadata["structuredMetadata"] = entry.StructuredMetadata
				}
				if len(entry.Parsed) > 0 {
					metadata["parsed"] = entry.Parsed
				}
				if len(metadata) > 0 {
					values[i] = append(values[i], metadata)
				}
			}
			formattedResults = append(formattedResults, map[string]any{
				"stream": stream.Labels,
				"values": values,
			})
		}
		finalResult = formattedResults
	case loghttp.ResultTypeMatrix:
		var formattedMatrix []map[string]any
		for _, matrixEntry := range mergedMatrix {
			values := make([][]any, len(matrixEntry.Values))
			for i, value := range matrixEntry.Values {
				values[i] = []any{value.Timestamp.Unix(), value.Value}
			}
			formattedMatrix = append(formattedMatrix, map[string]any{
				"metric": matrixEntry.Metric,
				"values": values,
			})
		}
		finalResult = formattedMatrix
	case loghttp.ResultTypeVector:
		var formattedVector []map[string]any
		for _, vectorEntry := range mergedVector {
			formattedVector = append(formattedVector, map[string]any{
				"metric": vectorEntry.Metric,
				"value":  []any{vectorEntry.Timestamp, vectorEntry.Value},
			})
		}
		finalResult = formattedVector
	}

	finalResponse := map[string]any{
		"status": "success",
		"data": map[string]any{
			"resultType": resultType,
			"result":     finalResult,
			"stats":      mergedStats,
		},
	}

	var encodingFlags []string
	for flag := range encodingFlagsMap {
		encodingFlags = append(encodingFlags, flag)
	}
	if len(encodingFlags) > 0 {
		finalResponse["data"].(map[string]any)["encodingFlags"] = encodingFlags
	}

	_ = json.NewEncoder(w).Encode(finalResponse)
}
