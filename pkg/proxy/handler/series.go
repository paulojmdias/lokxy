package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
)

func HandleLokiSeries(w http.ResponseWriter, results <-chan *http.Response, logger log.Logger) {
	var mergedSeries []map[string]string // Assuming series is a map of labels

	for resp := range results {
		defer resp.Body.Close()

		// Read the entire body
		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			if logErr := level.Error(logger).Log("msg", "Failed to read response body", "err", err); logErr != nil {
				fmt.Println("Error logging failure:", logErr)
			}
			continue
		}

		// Decode the response body into the expected series format
		var queryResult struct {
			Data   []map[string]string `json:"data"`
			Status string              `json:"status"`
		}
		if err := json.Unmarshal(bodyBytes, &queryResult); err != nil {
			if logErr := level.Error(logger).Log("msg", "Failed to unmarshal Loki series response", "err", err); logErr != nil {
				fmt.Println("Error logging failure:", logErr)
			}
			continue
		}

		// Append the series from this response to the mergedSeries
		mergedSeries = append(mergedSeries, queryResult.Data...)
	}

	// Log the merged series for debugging purposes
	_ = level.Debug(logger).Log("msg", "Merged series", "series", mergedSeries)

	// Prepare final response
	finalResponse := map[string]interface{}{
		"status": "success",
		"data":   mergedSeries,
	}

	// Log the answer series for debugging purposes
	_ = level.Debug(logger).Log("msg", "Grafana Answer", "series", finalResponse)

	if err := json.NewEncoder(w).Encode(finalResponse); err != nil {
		if logErr := level.Error(logger).Log("msg", "Failed to encode final response", "err", err); logErr != nil {
			fmt.Println("Error logging failure:", logErr)
		}
	}
}
