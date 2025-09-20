package handler

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	proxyErrors "github.com/paulojmdias/lokxy/pkg/proxy/errors"
)

// HandleLokiSeries handles /series requests
func HandleLokiSeries(w http.ResponseWriter, results <-chan *http.Response, logger log.Logger) {
	var mergedSeries []map[string]string
	hadResponse := false

	for resp := range results {
		defer resp.Body.Close()
		hadResponse = true

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

		var queryResult struct {
			Data   []map[string]string `json:"data"`
			Status string              `json:"status"`
		}
		if err := json.Unmarshal(bodyBytes, &queryResult); err != nil {
			level.Error(logger).Log("msg", proxyErrors.ErrUnmarshalFailed.Error(), "err", err)
			proxyErrors.WriteJSONError(w, http.StatusBadGateway, proxyErrors.ErrUnmarshalFailed.Error())
			return
		}

		mergedSeries = append(mergedSeries, queryResult.Data...)
	}

	if !hadResponse {
		proxyErrors.WriteJSONError(w, http.StatusBadGateway, proxyErrors.ErrNoUpstream.Error())
		return
	}

	finalResponse := map[string]any{
		"status": "success",
		"data":   mergedSeries,
	}
	if err := json.NewEncoder(w).Encode(finalResponse); err != nil {
		level.Error(logger).Log("msg", proxyErrors.ErrForwardingFailed.Error(), "err", err)
		proxyErrors.WriteJSONError(w, http.StatusInternalServerError, proxyErrors.ErrForwardingFailed.Error())
	}
}
