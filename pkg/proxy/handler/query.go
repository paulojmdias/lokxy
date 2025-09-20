package handler

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	proxyErrors "github.com/paulojmdias/lokxy/pkg/proxy/errors"
)

// QueryResponse represents Loki's /query and /query_range API response
type QueryResponse struct {
	Status string    `json:"status"`
	Data   QueryData `json:"data"`
}

type QueryData struct {
	ResultType string          `json:"resultType"`
	Result     json.RawMessage `json:"result"`
	Stats      json.RawMessage `json:"stats,omitempty"`
}

// HandleLokiQueries handles /query and /query_range requests
func HandleLokiQueries(w http.ResponseWriter, results <-chan *http.Response, logger log.Logger) {
	hadResponse := false

	for resp := range results {
		hadResponse = true
		defer resp.Body.Close()

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

		var queryResult QueryResponse
		if err := json.Unmarshal(bodyBytes, &queryResult); err != nil {
			level.Error(logger).Log("msg", proxyErrors.ErrUnmarshalFailed.Error(), "err", err)
			proxyErrors.WriteJSONError(w, http.StatusBadGateway, proxyErrors.ErrUnmarshalFailed.Error())
			return
		}

		if err := json.NewEncoder(w).Encode(queryResult); err != nil {
			level.Error(logger).Log("msg", proxyErrors.ErrForwardingFailed.Error(), "err", err)
			proxyErrors.WriteJSONError(w, http.StatusInternalServerError, proxyErrors.ErrForwardingFailed.Error())
		}
		return
	}

	if !hadResponse {
		proxyErrors.WriteJSONError(w, http.StatusBadGateway, proxyErrors.ErrNoUpstream.Error())
	}
}
