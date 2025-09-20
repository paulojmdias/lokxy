package errors

import (
	"encoding/json"
	"net/http"
)

// Sentinel errors for common cases
var (
	ErrNoUpstream            = New("no upstream Loki instances available")
	ErrInvalidResponse       = New("invalid response from upstream Loki")
	ErrReadBodyFailed        = New("failed to read upstream response body")
	ErrUnmarshalFailed       = New("failed to parse upstream JSON response")
	ErrForwardingFailed      = New("failed to forward request to upstream")
	ErrStatsNotSupported     = New("stats endpoint not supported by upstream Loki")
	ErrTailRequiresWebSocket = New("tail endpoint requires a WebSocket client")
	ErrUpgradeFailed         = New("failed to upgrade WebSocket connection")
	ErrBackendDialFailed     = New("failed to connect to backend WebSocket")
	ErrReadMessageFailed     = New("failed to read message from backend WebSocket")
	ErrWriteMessageFailed    = New("failed to write message to client WebSocket")
)

// New is just a wrapper to keep consistency
func New(msg string) error {
	return &sentinelError{msg}
}

type sentinelError struct {
	msg string
}

func (e *sentinelError) Error() string {
	return e.msg
}

// WriteJSONError ensures error responses are consistent
func WriteJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status":  "error",
		"message": msg,
	})
}
