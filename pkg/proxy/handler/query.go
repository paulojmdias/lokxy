package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/grafana/loki/v3/pkg/loghttp"
	"github.com/grafana/loki/v3/pkg/logqlmodel/stats" // For statistics
	"github.com/prometheus/common/model"
)

// Handle Loki query and query_range responses
func HandleLokiQueries(ctx context.Context, w http.ResponseWriter, results <-chan *http.Response, logger log.Logger) {
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
				if entry.StructuredMetadata.Len() > 0 {
					metadata["structuredMetadata"] = entry.StructuredMetadata
				}

				// Add parsed if it exists
				if entry.Parsed.Len() > 0 {
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
		// Apply downsampling if step override is active and original step > configured step
		if stepInfo, ok := GetStepInfo(ctx); ok {
			if stepInfo.OriginalStep > stepInfo.ConfiguredStep && stepInfo.OriginalStep > 0 {
				mergedMatrix = downsampleMatrix(mergedMatrix, stepInfo.OriginalStep, logger)
			}
		}

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

// downsampleMatrix downsamples matrix data to match the target step.
// It aligns timestamps to step boundaries and takes the last value in each bucket.
// This ensures compatibility with Grafana's lokiQuerySplitting feature.
func downsampleMatrix(matrix loghttp.Matrix, targetStep time.Duration, logger log.Logger) loghttp.Matrix {
	if targetStep <= 0 {
		return matrix
	}

	targetStepMs := targetStep.Milliseconds()
	result := make(loghttp.Matrix, 0, len(matrix))

	for _, series := range matrix {
		if len(series.Values) == 0 {
			result = append(result, series)
			continue
		}

		// Sort values by timestamp first
		sortedValues := make([]model.SamplePair, len(series.Values))
		copy(sortedValues, series.Values)
		sort.Slice(sortedValues, func(i, j int) bool {
			return sortedValues[i].Timestamp < sortedValues[j].Timestamp
		})

		// Group values into buckets aligned to step boundaries
		// and take the last value in each bucket
		buckets := make(map[int64]model.SamplePair)

		for _, sample := range sortedValues {
			sampleMs := int64(sample.Timestamp)
			// Align to step boundary (floor to nearest step)
			bucketTimestamp := (sampleMs / targetStepMs) * targetStepMs
			// Always keep the last value for each bucket
			buckets[bucketTimestamp] = model.SamplePair{
				Timestamp: model.Time(bucketTimestamp),
				Value:     sample.Value,
			}
		}

		// Convert map to sorted slice
		downsampled := make([]model.SamplePair, 0, len(buckets))
		for _, sample := range buckets {
			downsampled = append(downsampled, sample)
		}
		sort.Slice(downsampled, func(i, j int) bool {
			return downsampled[i].Timestamp < downsampled[j].Timestamp
		})

		// Create new series with downsampled values
		newSeries := model.SampleStream{
			Metric: series.Metric,
			Values: downsampled,
		}
		result = append(result, newSeries)
	}

	level.Debug(logger).Log(
		"msg", "Downsampled matrix data for Grafana alignment",
		"original_series", len(matrix),
		"target_step", targetStep.String(),
	)

	return result
}
