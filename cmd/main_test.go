package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/paulojmdias/lokxy/pkg/config"
	"github.com/paulojmdias/lokxy/pkg/o11y/logging"
	"github.com/paulojmdias/lokxy/pkg/o11y/metrics"
	"github.com/paulojmdias/lokxy/pkg/proxy"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func TestMainFunction(t *testing.T) {
	// Minimal config
	configContent := `
logging:
  level: info
  format: json
server_groups:
  - name: loki1
    url: http://dummy
    timeout: 10
`
	configFile, err := os.CreateTemp("", "config.yaml")
	if err != nil {
		t.Fatalf("Failed to create temp config file: %v", err)
	}
	defer os.Remove(configFile.Name())

	if _, err := configFile.Write([]byte(configContent)); err != nil {
		t.Fatalf("Failed to write to temp config file: %v", err)
	}
	configFile.Close()

	// Load configuration
	cfg, err := config.LoadConfig(configFile.Name())
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	logger := logging.ConfigureLogger(cfg.Logging)

	// Initialize Prometheus metrics
	ctx := context.Background()
	meterProvider, err := metrics.InitMetrics(ctx)
	if err != nil {
		t.Fatalf("Failed to initialize Prometheus metrics: %v", err)
	}
	defer func() {
		_ = meterProvider.Shutdown(ctx)
	}()

	// --- Metrics endpoint test (direct) ---
	t.Run("test metrics endpoint", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/metrics", nil)
		w := httptest.NewRecorder()

		promhttp.Handler().ServeHTTP(w, req)

		resp := w.Result()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected 200, got %d", resp.StatusCode)
		}
	})

	// --- Proxy handler success ---
	t.Run("test proxy handler (success)", func(t *testing.T) {
		backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"success"}`))
		}))
		defer backend.Close()

		cfg.ServerGroups[0].URL = backend.URL

		req := httptest.NewRequest("GET", "/loki/api/v1/query", nil)
		w := httptest.NewRecorder()

		proxy.ProxyHandler(w, req, cfg, logger)

		resp := w.Result()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected 200, got %d", resp.StatusCode)
		}
	})

	// --- Proxy handler failure ---
	t.Run("test proxy handler (failure)", func(t *testing.T) {
		cfg.ServerGroups[0].URL = "http://127.0.0.1:59999" // unreachable

		req := httptest.NewRequest("GET", "/loki/api/v1/query", nil)
		w := httptest.NewRecorder()

		proxy.ProxyHandler(w, req, cfg, logger)

		resp := w.Result()
		if resp.StatusCode != http.StatusBadGateway {
			t.Errorf("Expected 502, got %d", resp.StatusCode)
		}
	})
}
