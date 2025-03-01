package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"sort"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
)

func HandleLokiLabels(w http.ResponseWriter, results <-chan *http.Response, logger log.Logger) {
	mergedLabelValues := make(map[string]struct{})

	for resp := range results {
		bodyBytes, err := io.ReadAll(resp.Body)
		resp.Body.Close()

		if err != nil {
			_ = level.Error(logger).Log("msg", "Failed to read response body", "err", err)
			continue
		}

		// Log the raw body for debugging
		_ = level.Debug(logger).Log("msg", "Received body for label values", "body", string(bodyBytes))

		// Unmarshal into a struct that matches the actual response format
		var labelResponse struct {
			Status string   `json:"status"`
			Data   []string `json:"data"`
		}

		if err := json.Unmarshal(bodyBytes, &labelResponse); err != nil {
			_ = level.Error(logger).Log("msg", "Failed to unmarshal label values response", "err", err)
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
	finalResponse := map[string]interface{}{
		"status": "success",
		"data":   finalLabelValues,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(finalResponse); err != nil {
		_ = level.Error(logger).Log("msg", "Failed to encode final response for label values", "err", err)
	}
}
