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
	"sync"
	"syscall"
	"time"

	kitlog "github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"golang.org/x/sync/errgroup"

	"github.com/paulojmdias/lokxy/pkg/config"
	"github.com/paulojmdias/lokxy/pkg/o11y/logging"
	"github.com/paulojmdias/lokxy/pkg/o11y/metrics"
	traces "github.com/paulojmdias/lokxy/pkg/o11y/tracing"
	"github.com/paulojmdias/lokxy/pkg/proxy"
)

// Build information, populated at build-time
var (
	Version  string
	Revision string
)

// runOptions holds the CLI-derived settings run needs beyond the parsed config.
type runOptions struct {
	bindAddr        string
	metricsAddr     string
	configPath      string
	enableLifecycle bool
	watchConfig     bool
}

func main() {
	var opts runOptions
	// Parse flags
	flag.StringVar(&opts.bindAddr, "bind-addr", ":3100", "Address to bind the proxy server")
	flag.StringVar(&opts.configPath, "config", "config.yaml", "Path to the configuration file")
	flag.StringVar(&opts.metricsAddr, "metrics-addr", ":9091", "Address to bind the Prometheus metrics server")
	flag.BoolVar(&opts.enableLifecycle, "enable-lifecycle", false, "Enable reloading the configuration via the /-/reload HTTP endpoint")
	flag.BoolVar(&opts.watchConfig, "config.watch", false, "Watch the configuration file and reload it automatically when it changes")
	flag.Parse()

	// Load configuration
	cfg, err := config.LoadConfig(opts.configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Set up logging
	logger := logging.ConfigureLogger(cfg.Logging)

	// Create a context that cancels on SIGINT/SIGTERM
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Run lokxy
	if err := run(ctx, logger, cfg, opts); err != nil {
		level.Error(logger).Log("msg", "Failed to run", "err", err)
		os.Exit(1)
	}
}

// reloader re-reads the configuration file and applies it to the proxy. It
// serializes concurrent reload requests (HTTP endpoint, SIGHUP, file watcher)
// and records the outcome on the config reload metrics. A failed reload keeps
// the previous configuration active.
type reloader struct {
	mu      sync.Mutex
	path    string
	proxy   *proxy.Proxy
	logger  kitlog.Logger
	logging config.LoggerConfig // logging config at startup; changing it requires a restart
}

func (r *reloader) Reload(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	newCfg, err := config.LoadConfig(r.path)
	if err == nil {
		err = r.proxy.ApplyConfig(newCfg)
	}
	if err != nil {
		level.Error(r.logger).Log("msg", "Failed to reload configuration, keeping previous configuration", "path", r.path, "err", err)
		metrics.RecordConfigReload(ctx, false)
		return err
	}

	if newCfg.Logging != r.logging {
		level.Warn(r.logger).Log("msg", "Logging configuration changed on reload; logging changes require a restart to take effect")
	}
	metrics.RecordConfigReload(ctx, true)
	level.Info(r.logger).Log("msg", "Configuration reloaded", "path", r.path)
	return nil
}

func run(ctx context.Context, logger kitlog.Logger, cfg *config.Config, opts runOptions) error {
	// Startup log
	level.Info(logger).Log("msg", "Starting lokxy", "version", Version, "revision", Revision)

	// Listen addrs
	var lc net.ListenConfig
	metricsLn, err := lc.Listen(ctx, "tcp", opts.metricsAddr)
	if err != nil {
		return fmt.Errorf("failed to start metrics listener: %w", err)
	}
	defer func() {
		if err := metricsLn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			level.Error(logger).Log("msg", "Failed to stop metrics listener", "err", err)
		}
	}()

	proxyLn, err := lc.Listen(ctx, "tcp", opts.bindAddr)
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

	// Build the proxy. This fails fast on an invalid client configuration,
	// e.g. an unreadable TLS certificate file.
	p, err := proxy.New(logger, cfg)
	if err != nil {
		shutdownErr := meterProvider.Shutdown(ctx)
		if shutdownErr != nil {
			err = fmt.Errorf("%w (meter shutdown error: %v)", err, shutdownErr)
		}
		return fmt.Errorf("failed to build proxy: %w", err)
	}
	metrics.RecordConfigReload(ctx, true)

	rl := &reloader{
		path:    opts.configPath,
		proxy:   p,
		logger:  logger,
		logging: cfg.Logging,
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

	// Reload the configuration on SIGHUP.
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	eg.Go(func() error {
		defer signal.Stop(hup)
		for {
			select {
			case <-hup:
				level.Info(logger).Log("msg", "Received SIGHUP, reloading configuration")
				_ = rl.Reload(ctx) // errors are logged and the previous config stays active
			case <-ctx.Done():
				return nil
			}
		}
	})

	// Watch the configuration file for changes when enabled.
	if opts.watchConfig {
		eg.Go(func() error {
			return config.Watch(ctx, opts.configPath, logger, func() {
				_ = rl.Reload(ctx) // errors are logged and the previous config stays active
			})
		})
	}

	// Set up Lokxy proxy server
	reload := func() error { return rl.Reload(ctx) }
	proxyServer := &http.Server{Handler: traces.HTTPTracesHandler(logger)(proxy.NewServeMux(logger, p, reload, opts.enableLifecycle))}

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
