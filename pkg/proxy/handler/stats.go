package handler

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	proxyErrors "github.com/paulojmdias/lokxy/pkg/proxy/errors"
)

// HandleLokiStats aggregates stats responses from upstream Loki instances
func HandleLokiStats(w http.ResponseWriter, results <-chan *http.Response, logger log.Logger) {
	var totalStreams, totalChunks, totalBytes, totalEntries int
	hadResponse := false

	for resp := range results {
		defer resp.Body.Close()
		hadResponse = true

		// Special-case: stats endpoint not supported in some Loki versions
		if resp.StatusCode == http.StatusNotFound {
			level.Warn(logger).Log("msg", proxyErrors.ErrStatsNotSupported.Error())
			proxyErrors.WriteJSONError(w, http.StatusNotFound, proxyErrors.ErrStatsNotSupported.Error())
			return
		}

		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			level.Error(logger).Log("msg", proxyErrors.ErrReadBodyFailed.Error(), "err", err)
			proxyErrors.WriteJSONError(w, http.StatusBadGateway, proxyErrors.ErrReadBodyFailed.Error())
			return
		}

		if !json.Valid(bodyBytes) {
			proxyErrors.WriteJSONError(w, resp.StatusCode, string(bodyBytes))
			return
		}

		var statsResponse struct {
			Streams int `json:"streams"`
			Chunks  int `json:"chunks"`
			Bytes   int `json:"bytes"`
			Entries int `json:"entries"`
		}
		if err := json.Unmarshal(bodyBytes, &statsResponse); err != nil {
			level.Error(logger).Log("msg", proxyErrors.ErrUnmarshalFailed.Error(), "err", err)
			proxyErrors.WriteJSONError(w, http.StatusBadGateway, proxyErrors.ErrUnmarshalFailed.Error())
			return
		}

		totalStreams += statsResponse.Streams
		totalChunks += statsResponse.Chunks
		totalBytes += statsResponse.Bytes
		totalEntries += statsResponse.Entries
	}

	if !hadResponse {
		proxyErrors.WriteJSONError(w, http.StatusBadGateway, proxyErrors.ErrNoUpstream.Error())
		return
	}

	finalStatsResponse := map[string]any{
		"status":  "success",
		"streams": totalStreams,
		"chunks":  totalChunks,
		"bytes":   totalBytes,
		"entries": totalEntries,
	}
	if err := json.NewEncoder(w).Encode(finalStatsResponse); err != nil {
		level.Error(logger).Log("msg", proxyErrors.ErrForwardingFailed.Error(), "err", err)
		proxyErrors.WriteJSONError(w, http.StatusInternalServerError, proxyErrors.ErrForwardingFailed.Error())
	}
}
