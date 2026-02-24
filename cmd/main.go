package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	kitlog "github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/paulojmdias/lokxy/pkg/config"
	"github.com/paulojmdias/lokxy/pkg/o11y/logging"
	"github.com/paulojmdias/lokxy/pkg/o11y/metrics"
	traces "github.com/paulojmdias/lokxy/pkg/o11y/tracing"
	"github.com/paulojmdias/lokxy/pkg/proxy"
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

	// Create a context that cancels on SIGINT/SIGTERM
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Run lokxy
	if err := run(ctx, logger, cfg, bindAddr, metricsAddr); err != nil {
		level.Error(logger).Log("msg", "Failed to run", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, logger kitlog.Logger, cfg *config.Config, bindAddr, metricsAddr string) error {
	// Startup log
	level.Info(logger).Log("msg", "Starting lokxy", "version", Version, "revision", Revision)

	// Listen addrs
	var lc net.ListenConfig
	metricsLn, err := lc.Listen(ctx, "tcp", metricsAddr)
	if err != nil {
		return fmt.Errorf("failed to start metrics listener: %w", err)
	}
	defer func() {
		if err := metricsLn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			level.Error(logger).Log("msg", "Failed to stop metrics listener", "err", err)
		}
	}()

	proxyLn, err := lc.Listen(ctx, "tcp", bindAddr)
	if err != nil {
		return fmt.Errorf("failed to start proxy listener: %w", err)
	}
	defer func() {
		if err := proxyLn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			level.Error(logger).Log("msg", "Failed to stop proxy listener", "err", err)
		}
	}()

	// Initialize Prometheus metrics provider
	meterProvider, err := metrics.Initialize(ctx)
	if err != nil {
		return fmt.Errorf("failed to initialize Prometheus metrics: %w", err)
	}
	// Initialize tracer provider
	tracerProvider, err := traces.InitTracer(ctx)
	if err != nil {
		shutdownErr := meterProvider.Shutdown(ctx)
		return fmt.Errorf("failed to initialize tracer: %w (meter shutdown error: %v)", err, shutdownErr)
	}

	eg, ctx := errgroup.WithContext(ctx)
	// Set up Prometheus metrics server
	metricsServer := &http.Server{Handler: metrics.NewServeMux()}

	// Start the metrics server
	eg.Go(func() error {
		level.Info(logger).Log("msg", "Serving Prometheus metrics", "addr", metricsLn.Addr())
		if err := metricsServer.Serve(metricsLn); err != nil && !errors.Is(err, http.ErrServerClosed) {
			level.Error(logger).Log("msg", "Serving Prometheus metrics failed", "err", err)
			return err
		}
		return nil
	})

	// Set up Lokxy proxy server
	proxyServer := &http.Server{Handler: traces.HTTPTracesHandler(logger)(proxy.NewServeMux(logger, cfg))}

	// Start the proxy HTTP server
	eg.Go(func() error {
		level.Info(logger).Log("msg", "Listening", "addr", proxyLn.Addr())
		if err := proxyServer.Serve(proxyLn); !errors.Is(err, http.ErrServerClosed) {
			level.Error(logger).Log("msg", "Serving lokxy failed", "err", err)
			return err
		}
		return nil
	})

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
	return nil
}
