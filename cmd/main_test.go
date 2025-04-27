package main

import (
	"context"
	"net/http"
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

	// Set up logging
	logger := logging.ConfigureLogger(cfg.Logging)

	// Initialize Prometheus metrics
	ctx := context.Background()
	meterProvider, err := metrics.InitMetrics(ctx)
	if err != nil {
		t.Fatalf("Failed to initialize Prometheus metrics: %v", err)
	}
	defer meterProvider.Shutdown(ctx)

	// Create a test server with shutdown capability
	server := &http.Server{
		Addr:    ":9091",
		Handler: http.DefaultServeMux,
	}

	// Register Prometheus metrics handler
	http.Handle("/metrics", promhttp.Handler())

	// Register the proxy handler
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		proxy.ProxyHandler(w, r, cfg, logger)
	})

	// Channel to signal server shutdown
	serverClosed := make(chan struct{})

	// Start the server in a goroutine
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			t.Errorf("Server failed: %v", err)
		}
		close(serverClosed)
	}()

	// Test the /metrics endpoint
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

	// Test the proxy handler
	t.Run("test proxy handler", func(t *testing.T) {
		req, err := http.NewRequest("GET", "http://localhost:9091/", nil)
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

	// Shutdown the server
	if err := server.Shutdown(context.Background()); err != nil {
		t.Errorf("Failed to shutdown server: %v", err)
	}

	// Wait for server to close
	<-serverClosed
}
