package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/grafana/loki/v3/pkg/loghttp"
	"github.com/grafana/loki/v3/pkg/logqlmodel/stats" // For statistics
	"github.com/prometheus/common/model"

	"github.com/paulojmdias/lokxy/pkg/proxy/proxyresponse"
)

// encodingFlagsEnvelope is a lightweight struct for extracting encodingFlags
// without a full map[string]any unmarshal.
type encodingFlagsEnvelope struct {
	Data struct {
		EncodingFlags []string `json:"encodingFlags"`
	} `json:"data"`
}

// Handle Loki query and query_range responses
func HandleLokiQueries(_ context.Context, w http.ResponseWriter, results <-chan *proxyresponse.BackendResponse, logger log.Logger) {
	var mergedStreams []loghttp.Stream
	var mergedMatrix loghttp.Matrix
	var mergedVector loghttp.Vector
	var resultType loghttp.ResultType
	var mergedStats stats.Result
	encodingFlagsMap := make(map[string]struct{})
	vectorMap := make(map[model.Fingerprint]*model.Sample)
	matrixMap := make(map[model.Fingerprint]*model.SampleStream)

	// encodingFlagsMarker is used for a fast pre-check before the second
	// (targeted) unmarshal that extracts encoding flags.
	encodingFlagsMarker := []byte(`"encodingFlags"`)

	for backendResp := range results {
		resp := backendResp.Response
		// Read the entire body
		bodyBytes, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			level.Error(logger).Log("msg", "Failed to read response body", "err", err)
			continue
		}

		// Log the full body for debugging (guard to avoid string copy when debug is off)
		if ce := level.Debug(logger); ce != nil {
			_ = ce.Log("msg", "Complete body received", "body", string(bodyBytes))
		}

		// Single parse into loghttp.QueryResponse
		var queryResult loghttp.QueryResponse
		if err := json.Unmarshal(bodyBytes, &queryResult); err != nil {
			level.Error(logger).Log("msg", "Failed to unmarshal into loghttp.QueryResponse", "err", err)
			continue
		}

		// Extract encodingFlags only when the field is present in the raw
		// payload, avoiding a full second JSON parse in the common case.
		if bytes.Contains(bodyBytes, encodingFlagsMarker) {
			var envelope encodingFlagsEnvelope
			if err := json.Unmarshal(bodyBytes, &envelope); err == nil {
				for _, flag := range envelope.Data.EncodingFlags {
					encodingFlagsMap[flag] = struct{}{}
				}
			}
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
			for _, entry := range matrix {
				fp := entry.Metric.Fingerprint()
				if existing, exists := matrixMap[fp]; exists {
					existing.Values = sumMergeSamplePairs(existing.Values, entry.Values)
				} else {
					entryCopy := entry
					matrixMap[fp] = &entryCopy
				}
			}

		case loghttp.ResultTypeVector:
			vector, ok := queryResult.Data.Result.(loghttp.Vector)
			if !ok {
				level.Error(logger).Log("msg", "Failed to assert type to loghttp.Vector")
				continue
			}
			for _, sample := range vector {
				fp := sample.Metric.Fingerprint()
				if _, exists := vectorMap[fp]; !exists {
					sampleCopy := sample
					vectorMap[fp] = &sampleCopy
				}
			}
		}

		// Merge statistics
		mergedStats.Merge(queryResult.Data.Statistics)
	}

	// Convert maps to sorted slices for consistent output.  Pre-compute the
	// string sort key once per entry to avoid repeated allocations during sort.
	type vectorWithKey struct {
		sample model.Sample
		key    string
	}
	vectorKeyed := make([]vectorWithKey, 0, len(vectorMap))
	for _, sample := range vectorMap {
		vectorKeyed = append(vectorKeyed, vectorWithKey{
			sample: *sample,
			key:    modelMetricKey(sample.Metric),
		})
	}
	sort.Slice(vectorKeyed, func(i, j int) bool {
		return vectorKeyed[i].key < vectorKeyed[j].key
	})
	for _, vk := range vectorKeyed {
		mergedVector = append(mergedVector, vk.sample)
	}

	type matrixWithKey struct {
		stream model.SampleStream
		key    string
	}
	matrixKeyed := make([]matrixWithKey, 0, len(matrixMap))
	for _, entry := range matrixMap {
		matrixKeyed = append(matrixKeyed, matrixWithKey{
			stream: *entry,
			key:    modelMetricKey(entry.Metric),
		})
	}
	sort.Slice(matrixKeyed, func(i, j int) bool {
		return matrixKeyed[i].key < matrixKeyed[j].key
	})
	for _, mk := range matrixKeyed {
		mergedMatrix = append(mergedMatrix, mk.stream)
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

// modelMetricKey creates a consistent string key from a model.Metric for
// aggregation purposes. Labels are sorted by name so that two metrics with
// the same label pairs always produce the same key.
func modelMetricKey(m model.Metric) string {
	if len(m) == 0 {
		return ""
	}

	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, string(k))
	}
	sort.Strings(keys)

	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(string(m[model.LabelName(k)]))
	}
	return b.String()
}

// sumMergeSamplePairs merges two SamplePair slices that are already sorted by
// timestamp, summing values at the same timestamp and preserving chronological
// order.  Uses a two-pointer merge to avoid map overhead and re-sorting.
func sumMergeSamplePairs(existing, incoming []model.SamplePair) []model.SamplePair {
	if len(existing) == 0 {
		return incoming
	}
	if len(incoming) == 0 {
		return existing
	}

	merged := make([]model.SamplePair, 0, len(existing)+len(incoming))
	i, j := 0, 0

	for i < len(existing) && j < len(incoming) {
		switch {
		case existing[i].Timestamp < incoming[j].Timestamp:
			merged = append(merged, existing[i])
			i++
		case existing[i].Timestamp > incoming[j].Timestamp:
			merged = append(merged, incoming[j])
			j++
		default: // same timestamp: sum values
			merged = append(merged, model.SamplePair{
				Timestamp: existing[i].Timestamp,
				Value:     existing[i].Value + incoming[j].Value,
			})
			i++
			j++
		}
	}

	// Append remaining from whichever slice has leftovers.
	merged = append(merged, existing[i:]...)
	merged = append(merged, incoming[j:]...)

	return merged
}
