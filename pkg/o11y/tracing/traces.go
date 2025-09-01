package traces

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	"go.opentelemetry.io/otel/trace"
)

func InitTracer(ctx context.Context) (*sdktrace.TracerProvider, error) {
	// https://pkg.go.dev/go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc
	exporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithInsecure(),
		otlptracegrpc.WithEndpoint(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")),
	)
	if err != nil {
		return nil, err
	}

	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceNameKey.String("lokxy"),
		)),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)

	otel.SetTracerProvider(tracerProvider)

	// add context propagation
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return tracerProvider, nil
}

func CreateSpan(ctx context.Context, spanName string) (context.Context, trace.Span) {
	tracer := otel.Tracer("lokxy")
	return tracer.Start(ctx, spanName)
}

func ExtractTraceFromHTTPRequest(r *http.Request) context.Context {
	propagator := otel.GetTextMapPropagator()
	ctx := r.Context()
	return propagator.Extract(ctx, propagation.HeaderCarrier(r.Header))
}

func InjectTraceToHTTPRequest(ctx context.Context, r *http.Request) {
	propagator := otel.GetTextMapPropagator()
	propagator.Inject(ctx, propagation.HeaderCarrier(r.Header))
}

func HTTPTracesHandler(logger log.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ctx := ExtractTraceFromHTTPRequest(r)
			ctx, span := CreateSpan(ctx, fmt.Sprintf("%s %s", r.Method, r.URL.Path))
			defer span.End()

			// captures status code
			wrappedWriter := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
			r = r.WithContext(ctx)
			next.ServeHTTP(wrappedWriter, r)

			durationMs := float64(time.Since(start).Nanoseconds()) / 1e6

			// define attributes from requests to spans
			// from convention https://opentelemetry.io/docs/specs/semconv/http/http-spans/
			span.SetAttributes(
				attribute.String("http.request.method", r.Method),
				attribute.String("url.full", r.URL.String()),
				attribute.String("server.address", r.Host),
				attribute.String("user_agent.original", r.UserAgent()),
				attribute.String("client.address", r.RemoteAddr),
				attribute.Int("http.response.status_code", wrappedWriter.statusCode),
				attribute.Float64("http.request_duration_ms", durationMs),
				attribute.String("http.request.header.x-request-id", r.Header.Get("X-Request-ID")),
			)

			if wrappedWriter.statusCode >= 400 {
				span.SetStatus(codes.Error, fmt.Sprintf("HTTP %d", wrappedWriter.statusCode))
			}

			level.Info(logger).Log(
				"msg", "Request completed",
				"method", r.Method,
				"path", r.URL.Path,
				"status", wrappedWriter.statusCode,
				"duration_ms", durationMs,
			)
		})
	}
}

type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(statusCode int) {
	rw.statusCode = statusCode
	rw.ResponseWriter.WriteHeader(statusCode)
}

func (rw *responseWriter) Write(data []byte) (int, error) {
	return rw.ResponseWriter.Write(data)
}
