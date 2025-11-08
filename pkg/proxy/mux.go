package proxy

import (
	"net/http"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/paulojmdias/lokxy/pkg/config"
)

// NewServeMux returns an [http.ServeMux] preconfigured with a lokxy
// handlers.
//
// This function is typically used to mount a dedicated metrics server
// or to integrate metrics into an existing HTTP server.
func NewServeMux(logger log.Logger, cfg *config.Config) *http.ServeMux {
	proxyMux := http.NewServeMux()

	// Liveness probe endpoint
	proxyMux.HandleFunc("/healthy", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("OK")); err != nil {
			level.Error(logger).Log("msg", "Failed to write response in /healthy handler", "err", err)
		}
	})

	// Readiness probe endpoint
	proxyMux.HandleFunc("/ready", func(w http.ResponseWriter, _ *http.Request) {
		status := http.StatusOK
		msg := []byte("OK")
		if !config.IsReady() {
			status = http.StatusServiceUnavailable
			msg = []byte("Not Ready")
		}
		w.WriteHeader(status)
		if _, err := w.Write(msg); err != nil {
			level.Error(logger).Log("msg", "Failed to write response in /ready handler", "err", err)
		}
	})

	// Register the proxy handler for all other requests
	proxyMux.HandleFunc("/", proxyHandler(cfg, logger))
	return proxyMux
}
