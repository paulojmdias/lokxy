package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"sort"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
)

// DetectedLabel represents a single detected label entry from Loki
type DetectedLabel struct {
	Label       string `json:"label"`
	Cardinality int    `json:"cardinality"`
}

// LokiDetectedLabelsResponse represents the structure of the detected labels response from Loki
type LokiDetectedLabelsResponse struct {
	DetectedLabels []DetectedLabel `json:"detectedLabels"`
}

// HandleLokiDetectedLabels aggregates detected labels from multiple Loki instances
func HandleLokiDetectedLabels(w http.ResponseWriter, results <-chan *http.Response, logger log.Logger) {
	mergedLabels := make(map[string]int)

	for resp := range results {
		defer resp.Body.Close()

		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			level.Error(logger).Log("msg", "Failed to read response body", "err", err)
			continue
		}

		level.Debug(logger).Log("msg", "Received body for detected labels", "body", string(bodyBytes))

		var lokiResponse LokiDetectedLabelsResponse
		if err := json.Unmarshal(bodyBytes, &lokiResponse); err != nil {
			level.Error(logger).Log("msg", "Failed to unmarshal detected labels response", "err", err)
			continue
		}

		// Merge the detected labels - sum cardinalities
		for _, labelData := range lokiResponse.DetectedLabels {
			mergedLabels[labelData.Label] += labelData.Cardinality
		}
	}

	// Convert merged data to response format
	var finalDetectedLabels []DetectedLabel
	for label, cardinality := range mergedLabels {
		finalDetectedLabels = append(finalDetectedLabels, DetectedLabel{
			Label:       label,
			Cardinality: cardinality,
		})
	}

	// Sort labels for consistency
	sort.Slice(finalDetectedLabels, func(i, j int) bool {
		return finalDetectedLabels[i].Label < finalDetectedLabels[j].Label
	})

	// Prepare the final response in Loki format
	finalResponse := LokiDetectedLabelsResponse{
		DetectedLabels: finalDetectedLabels,
	}

	if err := json.NewEncoder(w).Encode(finalResponse); err != nil {
		level.Error(logger).Log("msg", "Failed to encode final detected labels response", "err", err)
	}
}
