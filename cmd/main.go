package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/go-kit/log/level"
	"github.com/paulojmdias/lokxy/pkg/config"
	"github.com/paulojmdias/lokxy/pkg/o11y/logging"
	"github.com/paulojmdias/lokxy/pkg/o11y/metrics"
	traces "github.com/paulojmdias/lokxy/pkg/o11y/tracing"
	"github.com/paulojmdias/lokxy/pkg/proxy"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/sync/errgroup"
)

// Build information, populated at build-time
var (
	Version  string
	Revision string
)

func main() {
	var bindAddr, configPath, metricsAddr string
	// Parse flags
	flag.StringVar(&bindAddr, "bind-addr", ":3100", "Address to bind the proxy server")
	flag.StringVar(&configPath, "config", "config.yaml", "Path to the configuration file")
	flag.StringVar(&metricsAddr, "metrics-addr", ":9091", "Address to bind the Prometheus metrics server")
	flag.Parse()

	// Load configuration
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Set up logging
	logger := logging.ConfigureLogger(cfg.Logging)

	// Startup log
	level.Info(logger).Log("msg", "Starting lokxy", "version", Version, "revision", Revision)

	// Create a context that cancels on SIGINT/SIGTERM
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var startWG sync.WaitGroup
	eg, ctx := errgroup.WithContext(ctx)

	// Initialize Prometheus metrics provider
	meterProvider, err := metrics.InitMetrics(ctx)
	if err != nil {
		log.Fatalf("Failed to initialize Prometheus metrics: %v", err)
	}
	// Initialize tracer provider
	tracerProvider, err := traces.InitTracer(ctx)
	if err != nil {
		log.Fatalf("Failed to initialize tracer: %v", err)
	}

	// Set up Prometheus metrics server
	//
	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", promhttp.Handler())
	metricsServer := &http.Server{Addr: metricsAddr, Handler: metricsMux}
	// Start the metrics server
	startWG.Add(1)
	eg.Go(func() error {
		level.Info(logger).Log("msg", "Serving Prometheus metrics", "addr", metricsAddr)
		startWG.Done()
		if err := metricsServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			level.Error(logger).Log("msg", "Serving Prometheus metrics failed", "err", err)
			return err
		}
		return nil
	})

	// Set up Lokxy proxy server
	//
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
	proxyMux.HandleFunc("/", proxy.ProxyHandler(cfg, logger))
	proxyServer := &http.Server{Addr: bindAddr, Handler: traces.HTTPTracesHandler(logger)(proxyMux)}

	// Start the proxy HTTP server
	startWG.Add(1)
	eg.Go(func() error {
		level.Info(logger).Log("msg", "Listening", "addr", bindAddr)
		startWG.Done()
		if err := proxyServer.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			level.Error(logger).Log("msg", "Serving lokxy failed", "err", err)
			return err
		}
		return nil
	})

	// Let other go routines to start http servers before changing the configuration
	startWG.Wait()
	// Set the application as ready
	config.SetReady(true)
	level.Info(logger).Log("msg", "Application is now ready to serve traffic")

	// Serve and wait for context cancellation
	<-ctx.Done()
	level.Info(logger).Log("msg", "Server is starting to exit...")

	// Shutdown
	config.SetReady(false)

	// Give outstanding requests some seconds to complete
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := proxyServer.Shutdown(shutdownCtx); err != nil {
		level.Error(logger).Log("msg", "Proxy server forced to shutdown", "err", err)
	}

	if err := metricsServer.Shutdown(shutdownCtx); err != nil {
		level.Error(logger).Log("msg", "Metrics server forced to shutdown", "err", err)
	}

	if err := eg.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		level.Error(logger).Log("msg", "Error during the shutdown", "err", err)
	}

	// Shutdown OTEL related services
	if err := tracerProvider.Shutdown(shutdownCtx); err != nil && !errors.Is(err, context.Canceled) {
		level.Error(logger).Log("msg", "Failed to shutdown tracer provider", "err", err)
	}

	if err := meterProvider.Shutdown(shutdownCtx); err != nil && !errors.Is(err, context.Canceled) {
		level.Error(logger).Log("msg", "Failed to shutdown meter provider", "err", err)
	}

	level.Info(logger).Log("msg", "Server exited")
}
