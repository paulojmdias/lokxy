package metrics

import (
	"context"
	"fmt"
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
)

var (
	// RequestCount counts the total number of requests processed by the proxy.
	// It is incremented once for every incoming request.
	RequestCount metric.Int64Counter = noop.Int64Counter{}

	// RequestDuration records the duration of each proxy request, measured
	// in seconds. It is observed once per completed request.
	RequestDuration metric.Float64Histogram = noop.Float64Histogram{}

	// RequestFailures counts the total number of requests that resulted in an
	// error or failure during processing.
	RequestFailures metric.Int64Counter = noop.Int64Counter{}
)

// Initialize prepares the OpenTelemetry metric pipeline for the service.
// It configures a Prometheus exporter, sets up a [sdkmetric.MeterProvider] with the
// appropriate resource information, and registers all metric instruments
// used by the application.
//
// If instrument initialization fails, the function returns a error describing
// both the initialization failure and any cleanup error.
//
// On success, it returns the configured MeterProvider, which the caller
// may shutdown during application shutdown.
//
// This function should be called during application startup before any
// metrics are recorded.
func Initialize(ctx context.Context) (*sdkmetric.MeterProvider, error) {
	promExporter, err := prometheus.New()
	if err != nil {
		return nil, err
	}

	// Use NewSchemaless to avoid schema version conflicts
	lokxyResource := resource.NewSchemaless(
		semconv.ServiceNameKey.String("lokxy"),
	)

	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(promExporter),
		sdkmetric.WithResource(lokxyResource),
	)

	otel.SetMeterProvider(meterProvider)

	err = createMetrics()
	if err != nil {
		shutdownErr := meterProvider.Shutdown(ctx)
		if shutdownErr != nil {
			err = fmt.Errorf("%w (meter shutdown error: %v)", err, shutdownErr)
		}
		return nil, err
	}

	return meterProvider, nil
}

// createMetrics initializes all meter instruments used to observe proxy
// behavior, including request volume, latency, and failure counts.
//
// If any metric instrument fails to initialize, the function returns
// an error describing which metric failed to be created. On success, all
// metric variables are populated and the function returns nil.
func createMetrics() error {
	var err error
	meter := otel.Meter("lokxy")
	RequestCount, err = meter.Int64Counter("lokxy_request_count_total",
		metric.WithDescription("Total number of requests processed by the proxy"),
	)
	if err != nil {
		return fmt.Errorf("failed to create RequestCount metric: %w", err)
	}

	RequestDuration, err = meter.Float64Histogram("lokxy_request_duration_seconds",
		metric.WithDescription("Duration of proxy requests in seconds"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return fmt.Errorf("failed to create RequestDuration metric: %w", err)
	}

	RequestFailures, err = meter.Int64Counter("lokxy_request_failures_total",
		metric.WithDescription("Total number of failed requests"),
	)
	if err != nil {
		return fmt.Errorf("failed to create RequestFailures metric: %w", err)
	}
	return nil
}

// NewServeMux returns an [http.ServeMux] preconfigured with a Prometheus
// metrics handler.
//
// This function is typically used to mount a dedicated metrics server
// or to integrate metrics into an existing HTTP server.
func NewServeMux() *http.ServeMux {
	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", promhttp.Handler())
	return metricsMux
}
