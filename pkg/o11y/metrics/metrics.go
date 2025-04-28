package metrics

import (
	"context"
	"log"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
)

var (
	RequestCount    metric.Int64Counter
	RequestDuration metric.Float64Histogram
	RequestFailures metric.Int64Counter
)

func InitMetrics(_ context.Context) (*sdkmetric.MeterProvider, error) {
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

	meter := otel.Meter("lokxy")

	RequestCount, err = meter.Int64Counter("lokxy_request_count_total",
		metric.WithDescription("Total number of requests processed by the proxy"),
	)
	if err != nil {
		log.Fatalf("failed to create RequestCount metric")
	}

	RequestDuration, err = meter.Float64Histogram("lokxy_request_duration_seconds",
		metric.WithDescription("Duration of proxy requests in seconds"),
		metric.WithUnit("s"),
	)
	if err != nil {
		log.Fatalf("Failed to create RequestDuration metric: %v", err)
	}

	RequestFailures, err = meter.Int64Counter("lokxy_request_failures_total",
		metric.WithDescription("Total number of failed requests"),
	)
	if err != nil {
		log.Fatalf("Failed to create RequestFailures metric: %v", err)
	}

	return meterProvider, nil
}
