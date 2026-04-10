package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sort"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"

	"github.com/paulojmdias/lokxy/pkg/proxy/proxyresponse"
)

// isFieldAllowed reports whether fieldName should be included in the label list.
// Returns true unconditionally when allowed is empty (expose all fields).
func isFieldAllowed(fieldName string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, f := range allowed {
		if f == fieldName {
			return true
		}
	}
	return false
}

func HandleLokiLabels(_ context.Context, w http.ResponseWriter, results <-chan *proxyresponse.BackendResponse, logger log.Logger) {
	mergedLabelValues := make(map[string]struct{})

	for backendResp := range results {
		resp := backendResp.Response
		bodyBytes, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			level.Error(logger).Log("msg", "Failed to read response body", "err", err)
			continue
		}

		// Log the raw body for debugging
		level.Debug(logger).Log("msg", "Received body for label values", "body", string(bodyBytes))

		// Unmarshal into a struct that matches the actual response format
		var labelResponse struct {
			Status string   `json:"status"`
			Data   []string `json:"data"`
		}

		if err := json.Unmarshal(bodyBytes, &labelResponse); err != nil {
			level.Error(logger).Log("msg", "Failed to unmarshal label values response", "err", err)
			continue
		}

		// Merge the label values
		for _, value := range labelResponse.Data {
			mergedLabelValues[value] = struct{}{}
		}
	}

	// Prepare the merged list of label values
	finalLabelValues := make([]string, 0, len(mergedLabelValues))
	for value := range mergedLabelValues {
		finalLabelValues = append(finalLabelValues, value)
	}

	// Sort the final list for consistency
	sort.Strings(finalLabelValues)

	// Encode the final response
	finalResponse := map[string]any{
		"status": "success",
		"data":   finalLabelValues,
	}

	if err := json.NewEncoder(w).Encode(finalResponse); err != nil {
		level.Error(logger).Log("msg", "Failed to encode final response for label values", "err", err)
	}
}

// HandleLokiLabelsWithMetadata merges real Loki label names with the names of
// detected structured-metadata fields, exposing the latter as ordinary labels.
//
// labelsResults is the fan-out channel from /loki/api/v1/labels.
// detectedFieldsBytes is the pre-aggregated body from /loki/api/v1/detected_fields
// (produced by HandleLokiDetectedFields running against the same backends).
// allowedFields restricts which field names are injected; empty means all.
func HandleLokiLabelsWithMetadata(
	_ context.Context,
	w http.ResponseWriter,
	labelsResults <-chan *proxyresponse.BackendResponse,
	detectedFieldsBytes []byte,
	allowedFields []string,
	logger log.Logger,
) {
	mergedLabels := make(map[string]struct{})

	// 1. Collect real label names from all backends (same logic as HandleLokiLabels).
	for backendResp := range labelsResults {
		resp := backendResp.Response
		bodyBytes, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			level.Error(logger).Log("msg", "Failed to read labels response body", "err", err)
			continue
		}

		var labelResponse struct {
			Status string   `json:"status"`
			Data   []string `json:"data"`
		}
		if err := json.Unmarshal(bodyBytes, &labelResponse); err != nil {
			level.Error(logger).Log("msg", "Failed to unmarshal labels response", "err", err)
			continue
		}
		for _, v := range labelResponse.Data {
			mergedLabels[v] = struct{}{}
		}
	}

	// 2. Inject detected-field names that pass the allowlist filter.
	if len(detectedFieldsBytes) > 0 {
		var fieldsOut LokiDetectedFieldsOut
		if err := json.Unmarshal(detectedFieldsBytes, &fieldsOut); err == nil {
			for _, f := range fieldsOut.Fields {
				if isFieldAllowed(f.Label, allowedFields) {
					mergedLabels[f.Label] = struct{}{}
				}
			}
		} else {
			level.Debug(logger).Log("msg", "Failed to unmarshal detected_fields bytes for metadata-as-labels; skipping", "err", err)
		}
	}

	// 3. Build sorted list and return the standard Loki label response shape.
	finalLabels := make([]string, 0, len(mergedLabels))
	for v := range mergedLabels {
		finalLabels = append(finalLabels, v)
	}
	sort.Strings(finalLabels)

	finalResponse := map[string]any{
		"status": "success",
		"data":   finalLabels,
	}
	if err := json.NewEncoder(w).Encode(finalResponse); err != nil {
		level.Error(logger).Log("msg", "Failed to encode merged labels response", "err", err)
	}
}

// HandleLokiLabelValuesWithMetadataField merges real Loki label values with
// values from a detected structured-metadata field of the same name, so that
// metadata fields exposed via HandleLokiLabelsWithMetadata also return values.
//
// labelValuesResults is the fan-out channel from /loki/api/v1/label/{name}/values.
// detectedFieldValuesBytes is the pre-aggregated body from
// /loki/api/v1/detected_field/{name}/values.
func HandleLokiLabelValuesWithMetadataField(
	_ context.Context,
	w http.ResponseWriter,
	labelValuesResults <-chan *proxyresponse.BackendResponse,
	detectedFieldValuesBytes []byte,
	logger log.Logger,
) {
	mergedValues := make(map[string]struct{})

	// 1. Collect real label values from all backends.
	for backendResp := range labelValuesResults {
		resp := backendResp.Response
		bodyBytes, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			level.Error(logger).Log("msg", "Failed to read label values response body", "err", err)
			continue
		}

		var labelResponse struct {
			Status string   `json:"status"`
			Data   []string `json:"data"`
		}
		if err := json.Unmarshal(bodyBytes, &labelResponse); err != nil {
			level.Error(logger).Log("msg", "Failed to unmarshal label values response", "err", err)
			continue
		}
		for _, v := range labelResponse.Data {
			mergedValues[v] = struct{}{}
		}
	}

	// 2. Merge detected-field values.
	if len(detectedFieldValuesBytes) > 0 {
		var fieldValuesOut LokiDetectedFieldValuesResponse
		if err := json.Unmarshal(detectedFieldValuesBytes, &fieldValuesOut); err == nil {
			for _, v := range fieldValuesOut.Values {
				mergedValues[v.Value] = struct{}{}
			}
		} else {
			level.Debug(logger).Log("msg", "Failed to unmarshal detected_field values bytes for metadata-as-labels; skipping", "err", err)
		}
	}

	// 3. Return sorted, deduplicated values in the standard Loki label-values shape.
	finalValues := make([]string, 0, len(mergedValues))
	for v := range mergedValues {
		finalValues = append(finalValues, v)
	}
	sort.Strings(finalValues)

	finalResponse := map[string]any{
		"status": "success",
		"data":   finalValues,
	}
	if err := json.NewEncoder(w).Encode(finalResponse); err != nil {
		level.Error(logger).Log("msg", "Failed to encode merged label values response", "err", err)
	}
}
