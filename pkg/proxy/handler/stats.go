package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
)

func HandleLokiStats(_ context.Context, w http.ResponseWriter, results <-chan *http.Response, logger log.Logger) {
	var totalStreams, totalChunks, totalBytes, totalEntries int

	for resp := range results {
		// Read the entire body
		bodyBytes, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			level.Error(logger).Log("msg", "Failed to read response body", "err", err)
			continue
		}

		// Parse the stats response
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

		// Sum stats from each endpoint
		totalStreams += statsResponse.Streams
		totalChunks += statsResponse.Chunks
		totalBytes += statsResponse.Bytes
		totalEntries += statsResponse.Entries
	}

	// Prepare final merged stats response
	finalStatsResponse := map[string]any{
		"streams": totalStreams,
		"chunks":  totalChunks,
		"bytes":   totalBytes,
		"entries": totalEntries,
	}

	// Send the merged stats response back to the client
	if err := json.NewEncoder(w).Encode(finalStatsResponse); err != nil {
		level.Error(logger).Log("msg", "Failed to encode final response", "err", err)
	}
}
