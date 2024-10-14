package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/paulojmdias/lokxy/pkg/config"
	"github.com/paulojmdias/lokxy/pkg/o11y/logging"
	"github.com/paulojmdias/lokxy/pkg/o11y/metrics"
	"github.com/paulojmdias/lokxy/pkg/proxy"
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

	// Set up logging
	logger := logging.ConfigureLogger(cfg.Logging)

	// Initialize Prometheus metrics
	metrics.InitMetrics()

	// Register Prometheus metrics handler
	http.Handle("/metrics", metrics.PrometheusHandler())

	// Register the proxy handler for all other requests
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		proxy.ProxyHandler(w, r, cfg, logger)
	})

	// Test the /metrics endpoint
	req, err := http.NewRequest("GET", "/metrics", nil)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}
	rr := httptest.NewRecorder()
	http.DefaultServeMux.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("Handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	// Test the proxy handler
	req, err = http.NewRequest("GET", "/", nil)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}
	rr = httptest.NewRecorder()
	http.DefaultServeMux.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("Handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}
}
