package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strconv"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
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
	var mergedVolumes []Volume
	volumeMap := make(map[string]*Volume)

	for resp := range results {
		defer resp.Body.Close()

		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			level.Error(logger).Log("msg", "Failed to read response body", "err", err)
			continue
		}

		level.Debug(logger).Log("msg", "Received body for volume", "body", string(bodyBytes))

		var volumeResponse VolumeResponse
		if err := json.Unmarshal(bodyBytes, &volumeResponse); err != nil {
			level.Error(logger).Log("msg", "Failed to unmarshal volume response", "err", err)
			continue
		}

		// Merge volumes by metric labels
		for _, volume := range volumeResponse.Data.Result {
			// Create a key from metric labels for deduplication/aggregation
			metricKey := createMetricKey(volume.Metric)

			if existingVolume, exists := volumeMap[metricKey]; exists {
				// Aggregate values - sum volume data
				if volumeResponse.Data.ResultType == "vector" {
					// For vector responses, sum the values
					if len(volume.Value) >= 2 && len(existingVolume.Value) >= 2 {
						existingVal := parseVolumeValue(existingVolume.Value[1])
						newVal := parseVolumeValue(volume.Value[1])
						summedValue := existingVal + newVal
						existingVolume.Value[1] = strconv.FormatInt(summedValue, 10)
					}
				} else if volumeResponse.Data.ResultType == "matrix" {
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
	resultType := "vector"
	if len(mergedVolumes) > 0 && len(mergedVolumes[0].Values) > 0 {
		resultType = "matrix"
	}

	// Prepare the final response
	finalResponse := VolumeResponse{
		Status: "success",
		Data: VolumeData{
			ResultType: resultType,
			Result:     mergedVolumes,
		},
	}

	if err := json.NewEncoder(w).Encode(finalResponse); err != nil {
		level.Error(logger).Log("msg", "Failed to encode final volume response", "err", err)
	}
}

// HandleLokiVolumeRange handles the volume_range endpoint
func HandleLokiVolumeRange(w http.ResponseWriter, results <-chan *http.Response, logger log.Logger) {
	// Volume range always returns matrix format
	var mergedVolumes []Volume
	volumeMap := make(map[string]*Volume)

	for resp := range results {
		defer resp.Body.Close()

		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			level.Error(logger).Log("msg", "Failed to read response body", "err", err)
			continue
		}

		level.Debug(logger).Log("msg", "Received body for volume_range", "body", string(bodyBytes))

		var volumeResponse VolumeResponse
		if err := json.Unmarshal(bodyBytes, &volumeResponse); err != nil {
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
		Status: "success",
		Data: VolumeData{
			ResultType: "matrix",
			Result:     mergedVolumes,
		},
	}

	if err := json.NewEncoder(w).Encode(finalResponse); err != nil {
		level.Error(logger).Log("msg", "Failed to encode final volume_range response", "err", err)
	}
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

	key := ""
	for i, k := range keys {
		if i > 0 {
			key += ","
		}
		key += k + "=" + metric[k]
	}
	return key
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
func mergeMatrixValues(existing, new [][]any) [][]any {
	if len(existing) == 0 {
		return new
	}
	if len(new) == 0 {
		return existing
	}

	// Create a map of timestamp -> value for existing data
	timestampMap := make(map[string]int64)
	for _, point := range existing {
		if len(point) >= 2 {
			timestamp := point[0]
			value := parseVolumeValue(point[1])
			if ts, ok := timestamp.(float64); ok {
				timestampMap[strconv.FormatFloat(ts, 'f', -1, 64)] = value
			}
		}
	}

	// Add/merge new data
	for _, point := range new {
		if len(point) >= 2 {
			timestamp := point[0]
			value := parseVolumeValue(point[1])
			if ts, ok := timestamp.(float64); ok {
				tsKey := strconv.FormatFloat(ts, 'f', -1, 64)
				timestampMap[tsKey] += value
			}
		}
	}

	// Convert back to array format and sort by timestamp
	result := make([][]any, 0, len(timestampMap))
	for tsKey, value := range timestampMap {
		if ts, err := strconv.ParseFloat(tsKey, 64); err == nil {
			result = append(result, []any{ts, strconv.FormatInt(value, 10)})
		}
	}

	// Sort by timestamp
	sort.Slice(result, func(i, j int) bool {
		ts1, _ := result[i][0].(float64)
		ts2, _ := result[j][0].(float64)
		return ts1 < ts2
	})

	return result
}
