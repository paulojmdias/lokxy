package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/grafana/loki/v3/pkg/loghttp"
	"github.com/grafana/loki/v3/pkg/logqlmodel/stats" // For statistics
)

// Handle Loki query and query_range responses
func HandleLokiQueries(_ context.Context, w http.ResponseWriter, results <-chan *http.Response, logger log.Logger) {
	var mergedStreams []loghttp.Stream
	var mergedMatrix loghttp.Matrix
	var mergedVector loghttp.Vector
	var resultType loghttp.ResultType
	var mergedStats stats.Result
	var encodingFlagsMap = make(map[string]struct{})

	for resp := range results {
		// Read the entire body
		bodyBytes, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			level.Error(logger).Log("msg", "Failed to read response body", "err", err)
			continue
		}

		// Log the full body for debugging
		level.Debug(logger).Log("msg", "Complete body received", "body", string(bodyBytes))

		// Decode into map[string]any to inspect the raw structure
		var rawBody map[string]any
		bodyStr := string(bodyBytes)
		if json.Valid(bodyBytes) {
			if err := json.Unmarshal(bodyBytes, &rawBody); err != nil {
				level.Error(logger).Log("msg", "Failed to decode JSON", "err", err)
			} else {
				level.Debug(logger).Log("msg", "Raw JSON body", "rawBody", bodyStr)
			}
		} else {
			level.Debug(logger).Log("msg", "Raw body is not JSON", "rawBody", bodyStr)
		}

		// Check if encodingFlags is present in the response and extract it
		if data, ok := rawBody["data"].(map[string]any); ok {
			if flags, ok := data["encodingFlags"].([]any); ok {
				for _, flag := range flags {
					if flagStr, ok := flag.(string); ok {
						encodingFlagsMap[flagStr] = struct{}{} // Add to map for uniqueness
					}
				}
			}
		}

		// Attempt to decode into the expected loghttp.QueryResponse structure
		var queryResult loghttp.QueryResponse
		if err := json.Unmarshal(bodyBytes, &queryResult); err != nil {
			level.Error(logger).Log("msg", "Failed to unmarshal into loghttp.QueryResponse", "err", err)
			continue
		}

		resultType = queryResult.Data.ResultType

		// Process based on ResultType
		switch queryResult.Data.ResultType {
		case loghttp.ResultTypeStream:
			streams, ok := queryResult.Data.Result.(loghttp.Streams)
			if !ok {
				level.Error(logger).Log("msg", "Failed to assert type to loghttp.Streams")
				continue
			}
			mergedStreams = append(mergedStreams, streams...)

		case loghttp.ResultTypeMatrix:
			matrix, ok := queryResult.Data.Result.(loghttp.Matrix)
			if !ok {
				level.Error(logger).Log("msg", "Failed to assert type to loghttp.Matrix")
				continue
			}
			mergedMatrix = append(mergedMatrix, matrix...)

		case loghttp.ResultTypeVector:
			vector, ok := queryResult.Data.Result.(loghttp.Vector)
			if !ok {
				level.Error(logger).Log("msg", "Failed to assert type to loghttp.Vector")
				continue
			}
			mergedVector = append(mergedVector, vector...)
		}

		// Merge statistics
		mergedStats.Merge(queryResult.Data.Statistics)
	}

	// Prepare final response
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

				// Create a map to hold both structuredMetadata and parsed, if they exist
				metadata := make(map[string]any)

				// Add structuredMetadata if it exists
				if len(entry.StructuredMetadata) > 0 {
					metadata["structuredMetadata"] = entry.StructuredMetadata
				}

				// Add parsed if it exists
				if len(entry.Parsed) > 0 {
					metadata["parsed"] = entry.Parsed
				}

				// If the metadata map is not empty, append it to the values
				if len(metadata) > 0 {
					values[i] = append(values[i], metadata)
				}
			}

			// Add each stream and its corresponding values to the result
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
				values[i] = []any{
					value.Timestamp.Unix(),
					value.Value,
				}
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
				"value": []any{
					vectorEntry.Timestamp,
					vectorEntry.Value,
				},
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

	// Convert map back to a slice of strings
	var encodingFlags []string
	for flag := range encodingFlagsMap {
		encodingFlags = append(encodingFlags, flag)
	}

	// Only add encodingFlags if it's defined in any of the responses
	if len(encodingFlags) > 0 {
		finalResponse["data"].(map[string]any)["encodingFlags"] = encodingFlags
	}

	if err := json.NewEncoder(w).Encode(finalResponse); err != nil {
		level.Error(logger).Log("msg", "Failed to encode final response", "err", err)
	}

}
