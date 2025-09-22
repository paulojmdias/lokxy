package handler

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
)

func HandleLokiSeries(w http.ResponseWriter, results <-chan *http.Response, logger log.Logger) {
	var mergedSeries []map[string]string

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

		var queryResult struct {
			Data   []map[string]string `json:"data"`
			Status string              `json:"status"`
		}
		if err := json.Unmarshal(bodyBytes, &queryResult); err != nil {
			level.Error(logger).Log("msg", "Failed to unmarshal Loki series response", "err", err)
			continue
		}

		mergedSeries = append(mergedSeries, queryResult.Data...)
	}

	finalResponse := map[string]any{
		"status": "success",
		"data":   mergedSeries,
	}

	_ = json.NewEncoder(w).Encode(finalResponse)
}
