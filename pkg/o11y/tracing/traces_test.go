package traces

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/go-kit/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	"go.opentelemetry.io/otel/trace"
)

func TestInitTracer(t *testing.T) {
	originalEndpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	defer func() {
		if originalEndpoint == "" {
			os.Unsetenv("OTEL_EXPORTER_OTLP_ENDPOINT")
		} else {
			os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", originalEndpoint)
		}
	}()

	os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "localhost:4317")

	ctx := context.Background()
	tracerProvider, err := InitTracer(ctx)

	if err != nil {
		t.Logf("InitTracer failed (expected in test env): %v", err)

		assert.Contains(t, err.Error(), "connection")
		return
	}

	require.NotNil(t, tracerProvider)

	globalProvider := otel.GetTracerProvider()
	assert.NotNil(t, globalProvider)

	propagator := otel.GetTextMapPropagator()
	assert.NotNil(t, propagator)

	defer func() {
		if tracerProvider != nil {
			tracerProvider.Shutdown(ctx)
		}
	}()
}

func TestInitTracerWithInvalidEndpoint(t *testing.T) {
	originalEndpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	defer func() {
		if originalEndpoint == "" {
			os.Unsetenv("OTEL_EXPORTER_OTLP_ENDPOINT")
		} else {
			os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", originalEndpoint)
		}
	}()

	os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "://invalid-scheme")

	ctx := context.Background()
	tracerProvider, err := InitTracer(ctx)

	if err != nil {
		t.Logf("Got expected error with malformed endpoint: %v", err)
		assert.Error(t, err)
	} else {
		t.Log("OTLP exporter was lenient with malformed endpoint")
		if tracerProvider != nil {
			tracerProvider.Shutdown(ctx)
		}
	}
}

func TestInitTracerWithEmptyEndpoint(t *testing.T) {
	originalEndpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	defer func() {
		if originalEndpoint == "" {
			os.Unsetenv("OTEL_EXPORTER_OTLP_ENDPOINT")
		} else {
			os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", originalEndpoint)
		}
	}()
	os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")

	ctx := context.Background()
	tracerProvider, err := InitTracer(ctx)

	if err != nil {
		t.Logf("Empty endpoint caused error: %v", err)
	} else {
		t.Log("OTLP exporter handled empty endpoint gracefully (using defaults)")
		require.NotNil(t, tracerProvider)

		globalProvider := otel.GetTracerProvider()
		assert.NotNil(t, globalProvider)

		tracerProvider.Shutdown(ctx)
	}
}

func TestInitTracerConfiguration(t *testing.T) {
	originalEndpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	defer func() {
		if originalEndpoint == "" {
			os.Unsetenv("OTEL_EXPORTER_OTLP_ENDPOINT")
		} else {
			os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", originalEndpoint)
		}
	}()

	os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "dummy:4317")

	ctx := context.Background()

	// Store original global state
	originalProvider := otel.GetTracerProvider()
	originalPropagator := otel.GetTextMapPropagator()

	defer func() {
		// Restore original state
		otel.SetTracerProvider(originalProvider)
		otel.SetTextMapPropagator(originalPropagator)
	}()

	tracerProvider, err := InitTracer(ctx)

	if err != nil {
		// Expected - connection will fail
		t.Logf("Connection failed as expected: %v", err)
	} else {
		// If it succeeds, clean up
		defer tracerProvider.Shutdown(ctx)
	}

	propagator := otel.GetTextMapPropagator()
	assert.NotNil(t, propagator)

	// Test that propagator works (basic smoke test)
	ctx2 := context.Background()
	headers := make(map[string]string)
	propagator.Inject(ctx2, propagation.MapCarrier(headers))

	extractedCtx := propagator.Extract(ctx2, propagation.MapCarrier(headers))
	assert.NotNil(t, extractedCtx)
}

func TestCreateSpan(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
		sdktrace.WithResource(testResource()),
	)
	otel.SetTracerProvider(tp)
	defer tp.Shutdown(context.Background())

	ctx := context.Background()
	spanName := "test-span"

	newCtx, span := CreateSpan(ctx, spanName)

	assert.NotNil(t, span)
	assert.NotEqual(t, ctx, newCtx)
	span.End()

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)

	recordedSpan := spans[0]
	assert.Equal(t, spanName, recordedSpan.Name)
	assert.Equal(t, "lokxy", recordedSpan.InstrumentationScope.Name)
}

func TestExtractTraceFromHTTPRequest(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	otel.SetTracerProvider(tp)
	defer tp.Shutdown(context.Background())

	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	ctx := context.Background()
	_, span := otel.Tracer("test").Start(ctx, "parent-span")
	spanCtx := span.SpanContext()

	req := httptest.NewRequest("GET", "/test", nil)
	propagator := otel.GetTextMapPropagator()
	propagator.Inject(trace.ContextWithSpan(ctx, span), propagation.HeaderCarrier(req.Header))

	extractedCtx := ExtractTraceFromHTTPRequest(req)

	extractedSpanCtx := trace.SpanContextFromContext(extractedCtx)
	assert.True(t, extractedSpanCtx.IsValid())
	assert.Equal(t, spanCtx.TraceID(), extractedSpanCtx.TraceID())

	span.End()
}

func TestInjectTraceToHTTPRequest(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	otel.SetTracerProvider(tp)
	defer tp.Shutdown(context.Background())

	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	ctx := context.Background()
	_, span := otel.Tracer("test").Start(ctx, "test-span")
	spanCtx := trace.ContextWithSpan(ctx, span)

	req := httptest.NewRequest("GET", "/test", nil)
	InjectTraceToHTTPRequest(spanCtx, req)

	traceParent := req.Header.Get("traceparent")
	assert.NotEmpty(t, traceParent)

	span.End()
}

func TestHTTPTracesHandler(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	otel.SetTracerProvider(tp)
	defer tp.Shutdown(context.Background())

	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	logger := log.NewNopLogger()

	testHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	tracedHandler := HTTPTracesHandler(logger)(testHandler)

	req := httptest.NewRequest("GET", "/api/test", nil)
	req.Header.Set("X-Request-ID", "test-request-123")
	req.Header.Set("User-Agent", "test-agent")

	rr := httptest.NewRecorder()
	tracedHandler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "OK", rr.Body.String())

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)

	span := spans[0]
	assert.Equal(t, "GET /api/test", span.Name)
	assert.Equal(t, codes.Ok, span.Status.Code)

	attrs := span.Attributes
	attrMap := make(map[string]any)
	for _, attr := range attrs {
		attrMap[string(attr.Key)] = attr.Value.AsInterface()
	}

	assert.Equal(t, "GET", attrMap["http.request.method"])
	assert.Contains(t, attrMap["url.full"], "/api/test")
	assert.Equal(t, "test-agent", attrMap["user_agent.original"])
	assert.Equal(t, int64(200), attrMap["http.response.status_code"])
	assert.Equal(t, "test-request-123", attrMap["http.request.header.x-request-id"])

	duration, exists := attrMap["http.request_duration_ms"]
	assert.True(t, exists)
	assert.IsType(t, float64(0), duration)
	assert.Greater(t, duration.(float64), 0.0)
}

func TestHTTPTracesHandlerWithError(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	otel.SetTracerProvider(tp)
	defer tp.Shutdown(context.Background())

	logger := log.NewNopLogger()

	errorHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Internal Server Error"))
	})

	tracedHandler := HTTPTracesHandler(logger)(errorHandler)

	req := httptest.NewRequest("POST", "/api/error", nil)
	rr := httptest.NewRecorder()

	tracedHandler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusInternalServerError, rr.Code)

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)

	span := spans[0]
	assert.Equal(t, "POST /api/error", span.Name)
	assert.Equal(t, codes.Error, span.Status.Code)
	assert.Equal(t, "HTTP 500", span.Status.Description)
}

func TestResponseWriter(t *testing.T) {
	rr := httptest.NewRecorder()
	rw := &responseWriter{ResponseWriter: rr, statusCode: http.StatusOK}

	rw.WriteHeader(http.StatusCreated)
	assert.Equal(t, http.StatusCreated, rw.statusCode)
	assert.Equal(t, http.StatusCreated, rr.Code)

	data := []byte("test response")
	n, err := rw.Write(data)

	require.NoError(t, err)
	assert.Equal(t, len(data), n)
	assert.Equal(t, string(data), rr.Body.String())
}

func TestResponseWriterDefaultStatusCode(t *testing.T) {
	rr := httptest.NewRecorder()
	rw := &responseWriter{ResponseWriter: rr, statusCode: http.StatusOK}
	data := []byte("test")
	rw.Write(data)

	assert.Equal(t, http.StatusOK, rw.statusCode)
}

func testResource() *resource.Resource {
	return resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceNameKey.String("lokxy"),
	)
}

// Benchmark tests
func BenchmarkCreateSpan(b *testing.B) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	otel.SetTracerProvider(tp)
	defer tp.Shutdown(context.Background())

	ctx := context.Background()

	for b.Loop() {
		_, span := CreateSpan(ctx, "benchmark-span")
		span.End()
	}
}

func BenchmarkHTTPTracesHandler(b *testing.B) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	otel.SetTracerProvider(tp)
	defer tp.Shutdown(context.Background())

	logger := log.NewNopLogger()

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	tracedHandler := HTTPTracesHandler(logger)(handler)

	for b.Loop() {
		req := httptest.NewRequest("GET", "/benchmark", nil)
		rr := httptest.NewRecorder()
		tracedHandler.ServeHTTP(rr, req)
	}
}
