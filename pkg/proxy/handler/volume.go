package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/paulojmdias/lokxy/pkg/o11y/metrics"
	traces "github.com/paulojmdias/lokxy/pkg/o11y/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
)

const (
	resultTypeVector = "vector"
	resultTypeMatrix = "matrix"
	statusSuccess    = "success"
)

// VolumeResponse represents the structure of the volume response from Loki
type VolumeResponse struct {
	Status string     `json:"status"`
	Data   VolumeData `json:"data"`
}

// VolumeData represents the volume data structure
type VolumeData struct {
	ResultType string   `json:"resultType"`
	Result     []Volume `json:"result"`
}

// Volume represents a single volume entry
type Volume struct {
	Metric map[string]string `json:"metric"`
	Value  []any             `json:"value"`  // [timestamp, value] for vector
	Values [][]any           `json:"values"` // [[timestamp, value], ...] for matrix
}

// HandleLokiVolume aggregates volume data from multiple Loki instances
func HandleLokiVolume(w http.ResponseWriter, results <-chan *http.Response, logger log.Logger) {
	ctx, span := traces.CreateSpan(context.Background(), "handle_volume")
	defer span.End()

	var mergedVolumes []Volume
	volumeMap := make(map[string]*Volume)

	for resp := range results {
		if resp == nil || resp.Body == nil {
			_, errSpan := traces.CreateSpan(ctx, "volume.nil_response")
			errSpan.RecordError(io.ErrUnexpectedEOF)
			errSpan.SetStatus(codes.Error, "nil upstream response/body")
			if metrics.RequestFailures != nil {
				metrics.RequestFailures.Add(ctx, 1, metric.WithAttributes(
					attribute.String("path", "/loki/api/v1/index/volume"),
					attribute.String("method", "GET"),
					attribute.String("error_type", "nil_response"),
				))
			}
			errSpan.End()
			level.Error(logger).Log("msg", "Nil upstream response/body for volume")
			continue
		}

		bodyBytes, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			_, errSpan := traces.CreateSpan(ctx, "volume.read_body")
			errSpan.RecordError(err)
			errSpan.SetStatus(codes.Error, "Failed to read response body")
			if metrics.RequestFailures != nil {
				metrics.RequestFailures.Add(ctx, 1, metric.WithAttributes(
					attribute.String("path", "/loki/api/v1/index/volume"),
					attribute.String("method", "GET"),
					attribute.String("error_type", "read_body_failed"),
				))
			}
			errSpan.End()
			level.Error(logger).Log("msg", "Failed to read response body", "err", err)
			continue
		}

		level.Debug(logger).Log("msg", "Received body for volume", "body", string(bodyBytes))

		var volumeResponse VolumeResponse
		if err := json.Unmarshal(bodyBytes, &volumeResponse); err != nil {
			_, errSpan := traces.CreateSpan(ctx, "volume.unmarshal")
			errSpan.RecordError(err)
			errSpan.SetStatus(codes.Error, "Failed to unmarshal volume response")
			if metrics.RequestFailures != nil {
				metrics.RequestFailures.Add(ctx, 1, metric.WithAttributes(
					attribute.String("path", "/loki/api/v1/index/volume"),
					attribute.String("method", "GET"),
					attribute.String("error_type", "json_unmarshal_failed"),
				))
			}
			errSpan.End()
			level.Error(logger).Log("msg", "Failed to unmarshal volume response", "err", err)
			continue
		}

		// Merge volumes by metric labels
		for _, volume := range volumeResponse.Data.Result {
			metricKey := createMetricKey(volume.Metric)

			if existingVolume, exists := volumeMap[metricKey]; exists {
				// Aggregate values - sum volume data
				if volumeResponse.Data.ResultType == resultTypeVector {
					// For vector responses, sum the values
					if len(volume.Value) >= 2 && len(existingVolume.Value) >= 2 {
						existingValue := parseVolumeValue(existingVolume.Value[1])
						newValue := parseVolumeValue(volume.Value[1])
						summedValue := existingValue + newValue
						existingVolume.Value[1] = strconv.FormatInt(summedValue, 10)
					}
				} else if volumeResponse.Data.ResultType == resultTypeMatrix {
					// For matrix responses, merge the values arrays
					existingVolume.Values = mergeMatrixValues(existingVolume.Values, volume.Values)
				}
			} else {
				// Add new volume entry
				volumeMap[metricKey] = &Volume{
					Metric: volume.Metric,
					Value:  volume.Value,
					Values: volume.Values,
				}
			}
		}
	}

	// Convert map back to slice and sort for consistency
	for _, volume := range volumeMap {
		mergedVolumes = append(mergedVolumes, *volume)
	}

	// Sort by metric key for consistent ordering
	sort.Slice(mergedVolumes, func(i, j int) bool {
		return createMetricKey(mergedVolumes[i].Metric) < createMetricKey(mergedVolumes[j].Metric)
	})

	// Determine result type - default to vector unless we have matrix data
	resultType := resultTypeVector
	if len(mergedVolumes) > 0 && len(mergedVolumes[0].Values) > 0 {
		resultType = resultTypeMatrix
	}

	// Prepare the final response
	finalResponse := VolumeResponse{
		Status: statusSuccess,
		Data: VolumeData{
			ResultType: resultType,
			Result:     mergedVolumes,
		},
	}

	_, encSpan := traces.CreateSpan(ctx, "volume.encode_response")
	if err := json.NewEncoder(w).Encode(finalResponse); err != nil {
		encSpan.RecordError(err)
		encSpan.SetStatus(codes.Error, "Failed to encode final volume response")
		level.Error(logger).Log("msg", "Failed to encode final volume response", "err", err)
	}
	encSpan.End()
}

// HandleLokiVolumeRange handles the volume_range endpoint
func HandleLokiVolumeRange(w http.ResponseWriter, results <-chan *http.Response, logger log.Logger) {
	ctx, span := traces.CreateSpan(context.Background(), "handle_volume_range")
	defer span.End()

	// Volume range always returns matrix format
	var mergedVolumes []Volume
	volumeMap := make(map[string]*Volume)

	for resp := range results {
		if resp == nil || resp.Body == nil {
			_, errSpan := traces.CreateSpan(ctx, "volume_range.nil_response")
			errSpan.RecordError(io.ErrUnexpectedEOF)
			errSpan.SetStatus(codes.Error, "nil upstream response/body")
			if metrics.RequestFailures != nil {
				metrics.RequestFailures.Add(ctx, 1, metric.WithAttributes(
					attribute.String("path", "/loki/api/v1/index/volume_range"),
					attribute.String("method", "GET"),
					attribute.String("error_type", "nil_response"),
				))
			}
			errSpan.End()
			level.Error(logger).Log("msg", "Nil upstream response/body for volume_range")
			continue
		}

		bodyBytes, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			_, errSpan := traces.CreateSpan(ctx, "volume_range.read_body")
			errSpan.RecordError(err)
			errSpan.SetStatus(codes.Error, "Failed to read response body")
			if metrics.RequestFailures != nil {
				metrics.RequestFailures.Add(ctx, 1, metric.WithAttributes(
					attribute.String("path", "/loki/api/v1/index/volume_range"),
					attribute.String("method", "GET"),
					attribute.String("error_type", "read_body_failed"),
				))
			}
			errSpan.End()
			level.Error(logger).Log("msg", "Failed to read response body", "err", err)
			continue
		}

		level.Debug(logger).Log("msg", "Received body for volume_range", "body", string(bodyBytes))

		var volumeResponse VolumeResponse
		if err := json.Unmarshal(bodyBytes, &volumeResponse); err != nil {
			_, errSpan := traces.CreateSpan(ctx, "volume_range.unmarshal")
			errSpan.RecordError(err)
			errSpan.SetStatus(codes.Error, "Failed to unmarshal volume_range response")
			if metrics.RequestFailures != nil {
				metrics.RequestFailures.Add(ctx, 1, metric.WithAttributes(
					attribute.String("path", "/loki/api/v1/index/volume_range"),
					attribute.String("method", "GET"),
					attribute.String("error_type", "json_unmarshal_failed"),
				))
			}
			errSpan.End()
			level.Error(logger).Log("msg", "Failed to unmarshal volume_range response", "err", err)
			continue
		}

		// Merge volumes by metric labels
		for _, volume := range volumeResponse.Data.Result {
			metricKey := createMetricKey(volume.Metric)

			if existingVolume, exists := volumeMap[metricKey]; exists {
				// For volume_range, merge the matrix values
				existingVolume.Values = mergeMatrixValues(existingVolume.Values, volume.Values)
			} else {
				// Add new volume entry
				volumeMap[metricKey] = &Volume{
					Metric: volume.Metric,
					Values: volume.Values,
				}
			}
		}
	}

	// Convert map back to slice and sort
	for _, volume := range volumeMap {
		mergedVolumes = append(mergedVolumes, *volume)
	}

	sort.Slice(mergedVolumes, func(i, j int) bool {
		return createMetricKey(mergedVolumes[i].Metric) < createMetricKey(mergedVolumes[j].Metric)
	})

	// Prepare the final response - always matrix for volume_range
	finalResponse := VolumeResponse{
		Status: statusSuccess,
		Data: VolumeData{
			ResultType: resultTypeMatrix,
			Result:     mergedVolumes,
		},
	}

	_, encSpan := traces.CreateSpan(ctx, "volume_range.encode_response")
	if err := json.NewEncoder(w).Encode(finalResponse); err != nil {
		encSpan.RecordError(err)
		encSpan.SetStatus(codes.Error, "Failed to encode final volume_range response")
		level.Error(logger).Log("msg", "Failed to encode final volume_range response", "err", err)
	}
	encSpan.End()
}

// createMetricKey creates a consistent key from metric labels for aggregation
func createMetricKey(metric map[string]string) string {
	if len(metric) == 0 {
		return ""
	}

	// Sort keys for consistency
	keys := make([]string, 0, len(metric))
	for k := range metric {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var key strings.Builder
	for i, k := range keys {
		if i > 0 {
			key.WriteByte(',')
		}
		key.WriteString(k)
		key.WriteByte('=')
		key.WriteString(metric[k])
	}
	return key.String()
}

// parseVolumeValue parses a volume value (could be string or number)
func parseVolumeValue(value any) int64 {
	switch v := value.(type) {
	case string:
		if val, err := strconv.ParseInt(v, 10, 64); err == nil {
			return val
		}
	case float64:
		return int64(v)
	case int64:
		return v
	case int:
		return int64(v)
	}
	return 0
}

// mergeMatrixValues merges two matrix value arrays, summing values at same timestamps
func mergeMatrixValues(existing, newValues [][]any) [][]any {
	if len(existing) == 0 {
		return newValues
	}
	if len(newValues) == 0 {
		return existing
	}

	// Create a map of timestamp -> value for existing data
	timestampMap := make(map[float64]int64)
	for _, point := range existing {
		if len(point) >= 2 {
			value := parseVolumeValue(point[1])
			if ts, ok := point[0].(float64); ok {
				timestampMap[ts] = value
			}
		}
	}

	// Add/merge newValues data
	for _, point := range newValues {
		if len(point) >= 2 {
			value := parseVolumeValue(point[1])
			if ts, ok := point[0].(float64); ok {
				timestampMap[ts] += value
			}
		}
	}

	// Collect and sort timestamps
	timestamps := make([]float64, 0, len(timestampMap))
	for ts := range timestampMap {
		timestamps = append(timestamps, ts)
	}
	sort.Float64s(timestamps)

	// Build sorted result array
	result := make([][]any, 0, len(timestamps))
	for _, ts := range timestamps {
		result = append(result, []any{ts, strconv.FormatInt(timestampMap[ts], 10)})
	}

	return result
}
