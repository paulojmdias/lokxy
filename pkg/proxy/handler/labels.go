package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"sort"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	proxyErrors "github.com/paulojmdias/lokxy/pkg/proxy/errors"
)

// HandleLokiLabels handles /labels requests
func HandleLokiLabels(w http.ResponseWriter, results <-chan *http.Response, logger log.Logger) {
	mergedLabelValues := make(map[string]struct{})
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

		var labelResponse struct {
			Status string   `json:"status"`
			Data   []string `json:"data"`
		}
		if err := json.Unmarshal(bodyBytes, &labelResponse); err != nil {
			level.Error(logger).Log("msg", proxyErrors.ErrUnmarshalFailed.Error(), "err", err)
			proxyErrors.WriteJSONError(w, http.StatusBadGateway, proxyErrors.ErrUnmarshalFailed.Error())
			return
		}

		for _, value := range labelResponse.Data {
			mergedLabelValues[value] = struct{}{}
		}
	}

	if !hadResponse {
		proxyErrors.WriteJSONError(w, http.StatusBadGateway, proxyErrors.ErrNoUpstream.Error())
		return
	}

	finalLabelValues := make([]string, 0, len(mergedLabelValues))
	for value := range mergedLabelValues {
		finalLabelValues = append(finalLabelValues, value)
	}
	sort.Strings(finalLabelValues)

	finalResponse := map[string]any{
		"status": "success",
		"data":   finalLabelValues,
	}
	if err := json.NewEncoder(w).Encode(finalResponse); err != nil {
		level.Error(logger).Log("msg", proxyErrors.ErrForwardingFailed.Error(), "err", err)
		proxyErrors.WriteJSONError(w, http.StatusInternalServerError, proxyErrors.ErrForwardingFailed.Error())
	}
}
