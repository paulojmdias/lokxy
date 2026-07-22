package proxy

import (
	"fmt"
	"net/http"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"

	"github.com/paulojmdias/lokxy/pkg/config"
)

// NewServeMux returns an [http.ServeMux] preconfigured with the lokxy
// handlers.
//
// reload is invoked by the /-/reload endpoint to re-read and apply the
// configuration; it is only reachable when enableLifecycle is true
// (mirroring Prometheus' --web.enable-lifecycle behavior).
func NewServeMux(logger log.Logger, p *Proxy, reload func() error, enableLifecycle bool) *http.ServeMux {
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

	// Configuration reload endpoint (Prometheus-style lifecycle API).
	// Registered explicitly for every method so a GET does not fall through
	// to the catch-all proxy handler.
	proxyMux.HandleFunc("/-/reload", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost && r.Method != http.MethodPut {
			w.Header().Set("Allow", "POST, PUT")
			http.Error(w, "method not allowed: use POST", http.StatusMethodNotAllowed)
			return
		}
		if !enableLifecycle || reload == nil {
			http.Error(w, "Lifecycle API is not enabled (--enable-lifecycle flag)", http.StatusForbidden)
			return
		}
		if err := reload(); err != nil {
			http.Error(w, fmt.Sprintf("failed to reload config: %s", err), http.StatusInternalServerError)
			return
		}
		if _, err := w.Write([]byte("config reloaded")); err != nil {
			level.Error(logger).Log("msg", "Failed to write response in /-/reload handler", "err", err)
		}
	})

	// Register the proxy handler for all other requests
	proxyMux.HandleFunc("/", p.Handler())
	return proxyMux
}
