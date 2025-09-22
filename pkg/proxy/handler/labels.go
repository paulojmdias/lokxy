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

		var labelResponse struct {
			Status string   `json:"status"`
			Data   []string `json:"data"`
		}
		if err := json.Unmarshal(bodyBytes, &labelResponse); err != nil {
			level.Error(logger).Log("msg", "Failed to unmarshal label values response", "err", err)
			continue
		}

		for _, value := range labelResponse.Data {
			mergedLabelValues[value] = struct{}{}
		}
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

	_ = json.NewEncoder(w).Encode(finalResponse)
}
