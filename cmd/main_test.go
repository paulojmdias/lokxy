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
	// Set up a temporary config file
	configContent := `
logging:
  level: info
  format: json
server_groups:
  - name: loki1
    url: http://loki1.example.com
    timeout: 10
    headers:
      Authorization: Bearer token1
  - name: loki2
    url: http://loki2.example.com
    timeout: 15
    headers:
      Authorization: Bearer token2
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
		if err := meterProvider.Shutdown(ctx); err != nil {
			t.Logf("Metrics shutdown error: %v", err)
		}
	}()

	// Set up metrics server
	metricsAddr := ":9091"
	metricServer := http.NewServeMux()
	metricServer.Handle("/metrics", promhttp.Handler())

	go func() {
		if err := http.ListenAndServe(metricsAddr, metricServer); err != nil && err != http.ErrServerClosed {
			t.Errorf("Metrics server failed: %v", err)
		}
	}()

	// Create main proxy server
	proxyServer := &http.Server{
		Addr:    ":3100",
		Handler: http.DefaultServeMux,
	}

	http.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("OK")); err != nil {
			t.Logf("failed to write health response: %v", err)
		}
	})
	http.HandleFunc("/healthy", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("Alive")); err != nil {
			t.Logf("failed to write healthy response: %v", err)
		}
	})

	http.HandleFunc("/ready", func(w http.ResponseWriter, _ *http.Request) {
		if config.IsReady() {
			w.WriteHeader(http.StatusOK)
			if _, err := w.Write([]byte("OK")); err != nil {
				t.Logf("failed to write readiness response: %v", err)
			}
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
			if _, err := w.Write([]byte("Not Ready")); err != nil {
				t.Logf("failed to write readiness response: %v", err)
			}
		}
	})

	// Register the proxy handler
	http.HandleFunc("/", proxy.ProxyHandler(cfg, logger))

	go func() {
		if err := proxyServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			t.Errorf("Proxy server failed: %v", err)
		}
	}()

	// Test cases
	t.Run("test metrics endpoint", func(t *testing.T) {
		resp, err := http.Get("http://localhost:9091/metrics")
		if err != nil {
			t.Fatalf("Failed to get metrics: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("Handler returned wrong status code: got %v want %v", resp.StatusCode, http.StatusOK)
		}
	})

	t.Run("test health endpoint", func(t *testing.T) {
		resp, err := http.Get("http://localhost:3100/health")
		if err != nil {
			t.Fatalf("Failed to get health: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("Handler returned wrong status code: got %v want %v", resp.StatusCode, http.StatusOK)
		}
	})

	t.Run("test proxy handler", func(t *testing.T) {
		backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer backend.Close()
		cfg.ServerGroups[0].URL = backend.URL

		req, err := http.NewRequest("GET", "http://localhost:3100/", nil)
		if err != nil {
			t.Fatalf("Failed to create request: %v", err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Failed to make request: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("Handler returned wrong status code: got %v want %v", resp.StatusCode, http.StatusOK)
		}
	})

	t.Run("test healthy endpoint", func(t *testing.T) {
		resp, err := http.Get("http://localhost:3100/healthy")
		if err != nil {
			t.Fatalf("Failed to get healthy: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("Handler returned wrong status code: got %v want %v", resp.StatusCode, http.StatusOK)
		}
	})

	t.Run("test readiness before ready", func(t *testing.T) {
		config.SetReady(false)
		resp, err := http.Get("http://localhost:3100/ready")
		if err != nil {
			t.Fatalf("Failed to get readiness: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Errorf("Expected not ready status, got: %v", resp.StatusCode)
		}
	})

	t.Run("test readiness after ready", func(t *testing.T) {
		config.SetReady(true)
		resp, err := http.Get("http://localhost:3100/ready")
		if err != nil {
			t.Fatalf("Failed to get readiness: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected ready status, got: %v", resp.StatusCode)
		}
	})

	if err := proxyServer.Shutdown(ctx); err != nil {
		t.Errorf("Failed to shutdown proxy server: %v", err)
	}
}
