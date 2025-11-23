package proxyresponse

import (
	"fmt"
	"net/http"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
)

// BackendResponse wraps an HTTP response with metadata about the backend
type BackendResponse struct {
	Response    *http.Response
	BackendName string
	BackendURL  string
}

// BackendError wraps an error with metadata about which backend caused it
type BackendError struct {
	Err         error
	BackendName string
	BackendURL  string
}

// ForwardBackendError sends a simple error response: {backend}: {error}
func ForwardBackendError(w http.ResponseWriter, backendName string, statusCode int, bodyBytes []byte, logger log.Logger) {
	level.Error(logger).Log(
		"msg", "Forwarding backend error to client",
		"backend", backendName,
		"status", statusCode,
		"body", string(bodyBytes),
	)

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(statusCode)

	errorMessage := fmt.Sprintf("%s: %s", backendName, string(bodyBytes))
	if _, err := w.Write([]byte(errorMessage)); err != nil {
		level.Error(logger).Log("msg", "Failed to write error response", "err", err)
	}
}

// ForwardConnectionError sends a connection error response: {backend}: {error}
// Uses 502 Bad Gateway to indicate the backend was unreachable
func ForwardConnectionError(w http.ResponseWriter, backendErr *BackendError, logger log.Logger) {
	level.Error(logger).Log(
		"msg", "Forwarding connection error to client",
		"backend", backendErr.BackendName,
		"err", backendErr.Err,
	)

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusBadGateway)

	errorMessage := fmt.Sprintf("%s: %s", backendErr.BackendName, backendErr.Err.Error())
	if _, err := w.Write([]byte(errorMessage)); err != nil {
		level.Error(logger).Log("msg", "Failed to write error response", "err", err)
	}
}
