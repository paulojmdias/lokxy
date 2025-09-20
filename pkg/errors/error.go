package errors

import (
	"encoding/json"
	"net/http"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
)

// ErrorResponse Standard error payload for Lokxy
type ErrorResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

// LokiErrorResponse Matches Loki error responses
type LokiErrorResponse struct {
	Status    string `json:"status"`
	ErrorType string `json:"errorType,omitempty"`
	Error     string `json:"error,omitempty"`
}

// WriteJSON sends an error in JSON format to the client.
func WriteJSON(w http.ResponseWriter, logger log.Logger, code int, msg string, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)

	resp := ErrorResponse{
		Status:  "error",
		Message: msg,
	}

	// Log full error
	if err != nil {
		level.Error(logger).Log("msg", msg, "err", err)
	} else {
		level.Error(logger).Log("msg", msg)
	}

	if e := json.NewEncoder(w).Encode(resp); e != nil {
		level.Error(logger).Log("msg", "Failed to encode error response", "err", e)
	}
}
