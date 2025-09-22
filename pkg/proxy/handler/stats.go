package handler

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
)

func HandleLokiStats(w http.ResponseWriter, results <-chan *http.Response, logger log.Logger) {
	var totalStreams, totalChunks, totalBytes, totalEntries int

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

		var statsResponse struct {
			Streams int `json:"streams"`
			Chunks  int `json:"chunks"`
			Bytes   int `json:"bytes"`
			Entries int `json:"entries"`
		}
		if err := json.Unmarshal(bodyBytes, &statsResponse); err != nil {
			level.Error(logger).Log("msg", "Failed to unmarshal stats response", "err", err)
			continue
		}

		totalStreams += statsResponse.Streams
		totalChunks += statsResponse.Chunks
		totalBytes += statsResponse.Bytes
		totalEntries += statsResponse.Entries
	}

	finalStatsResponse := map[string]any{
		"streams": totalStreams,
		"chunks":  totalChunks,
		"bytes":   totalBytes,
		"entries": totalEntries,
	}

	_ = json.NewEncoder(w).Encode(finalStatsResponse)
}
