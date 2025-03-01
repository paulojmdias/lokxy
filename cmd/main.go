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

	// Initialize Prometheus metrics
	metrics.InitMetrics()

	// Register Prometheus metrics handler
	http.Handle("/metrics", metrics.PrometheusHandler())

	// Register the proxy handler for all other requests
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		proxy.ProxyHandler(w, r, cfg, logger)
	})

	// Start the HTTP server
	if err := level.Info(logger).Log("msg", "Starting lokxy", "addr", bindAddr); err != nil {
		if logErr := level.Error(logger).Log("msg", "Failed to log start message", "err", err); logErr != nil {
			fmt.Println("Logging error:", logErr)
		}
	}

	if err := http.ListenAndServe(*bindAddr, nil); err != nil {
		if err := level.Info(logger).Log("msg", "Serving lokxy failed", "err", err); err != nil {
			if logErr := level.Error(logger).Log("msg", "Failed to log serve failure", "err", err); logErr != nil {
				fmt.Println("Logging error:", logErr)
			}
		}
	}
}
