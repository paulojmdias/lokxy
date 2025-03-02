package main

import (
	"flag"
	"fmt"
	"net/http"

	"github.com/go-kit/log/level"
	"github.com/paulojmdias/lokxy/pkg/config"
	"github.com/paulojmdias/lokxy/pkg/o11y/logging"
	"github.com/paulojmdias/lokxy/pkg/o11y/metrics"
	"github.com/paulojmdias/lokxy/pkg/proxy"
)

// Build information, populated at build-time
var (
	Version  string
	Revision string
)

func main() {
	// Parse flags
	bindAddr := flag.String("bind-addr", ":3100", "Address to bind the proxy server")
	configPath := flag.String("config", "config.yaml", "Path to the configuration file")
	flag.Parse()

	// Load configuration
	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		fmt.Println("Failed to load config:", err)
		return
	}

	// Set up logging
	logger := logging.ConfigureLogger(cfg.Logging)

	// Startup log
	level.Info(logger).Log("msg", "Starting lokxy", "version", Version, "revision", Revision)

	// Initialize Prometheus metrics
	metrics.InitMetrics()

	// Register Prometheus metrics handler
	http.Handle("/metrics", metrics.PrometheusHandler())

	// Register the proxy handler for all other requests
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		proxy.ProxyHandler(w, r, cfg, logger)
	})

	// Start the HTTP server
	level.Info(logger).Log("msg", "Listening", "addr", bindAddr)
	if err := http.ListenAndServe(*bindAddr, nil); err != nil {
		level.Info(logger).Log("msg", "Serving lokxy failed", "err", err)
	}
}
