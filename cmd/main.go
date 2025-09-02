package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/go-kit/log/level"
	"github.com/paulojmdias/lokxy/pkg/config"
	"github.com/paulojmdias/lokxy/pkg/o11y/logging"
	"github.com/paulojmdias/lokxy/pkg/o11y/metrics"
	traces "github.com/paulojmdias/lokxy/pkg/o11y/tracing"
	"github.com/paulojmdias/lokxy/pkg/proxy"
	"github.com/prometheus/client_golang/prometheus/promhttp"
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
	metricsAddr := flag.String("metrics-addr", ":9091", "Address to bind the Prometheus metrics server")
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

	ctx := context.Background()
	// Initialize Prometheus metrics
	meterProvider, err := metrics.InitMetrics(ctx)
	if err != nil {
		log.Fatalf("Failed to initialize Prometheus metrics: %v", err)
	}
	defer func() {
		if err := meterProvider.Shutdown(ctx); err != nil {
			log.Println(err)
		}
	}()

	// Set up Prometheus metrics server
	metricServer := http.NewServeMux()
	metricServer.Handle("/metrics", promhttp.Handler())

	// Start the metrics server
	go func() {
		level.Info(logger).Log("msg", "Serving Prometheus metrics", "addr", *metricsAddr)
		if err := http.ListenAndServe(*metricsAddr, metricServer); err != nil && err != http.ErrServerClosed {
			level.Error(logger).Log("msg", "Serving Prometheus metrics failed", "err", err)
		}
	}()

	// initialize tracer provider
	tracerProvider, err := traces.InitTracer(ctx)
	if err != nil {
		log.Fatalf("Failed to initialize tracer: %v", err)
	}
	defer func() {
		if err := tracerProvider.Shutdown(ctx); err != nil {
			log.Fatalf("Failed to shutdown tracer provider: %v", err)
		}
	}()

	// Register health check endpoint
	http.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("OK - Prometheus metrics enabled")); err != nil {
			log.Printf("failed to write response in /health handler: %v", err)
		}
	})

	//Liveness probe endpoint
	http.HandleFunc("/healthy", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("OK")); err != nil {
			level.Error(logger).Log("msg", "Failed to write response in /healthy handler", "err", err)
		}
	})

	// Readiness probe endpoint
	http.HandleFunc("/ready", func(w http.ResponseWriter, _ *http.Request) {
		if config.IsReady() {
			w.WriteHeader(http.StatusOK)
			if _, err := w.Write([]byte("OK")); err != nil {
				level.Error(logger).Log("msg", "Failed to write response in /ready handler", "err", err)
			}
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
			if _, err := w.Write([]byte("Not Ready")); err != nil {
				level.Error(logger).Log("msg", "Failed to write response in /ready handler", "err", err)
			}
		}
	})

	// Register the proxy handler for all other requests
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		ctx := traces.ExtractTraceFromHTTPRequest(r)

		// Add span for this request
		_, span := traces.CreateSpan(ctx, "proxy_request")
		defer span.End()

		proxy.ProxyHandler(w, r, cfg, logger)
	})

	go func() {
		time.Sleep(5 * time.Second)

		// Set the application as ready
		config.SetReady(true)
		level.Info(logger).Log("msg", "Application is now ready to serve traffic")
	}()

	// Start the HTTP server
	level.Info(logger).Log("msg", "Listening", "addr", *bindAddr)
	if err := http.ListenAndServe(*bindAddr, traces.HTTPTracesHandler(logger)(http.DefaultServeMux)); err != nil {
		level.Info(logger).Log("msg", "Serving lokxy failed", "err", err)
	}
}
