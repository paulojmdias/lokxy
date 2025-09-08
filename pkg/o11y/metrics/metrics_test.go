package metrics

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func TestInitMetrics(t *testing.T) {
	ctx := context.Background()

	mp, err := InitMetrics(ctx)
	if err != nil {
		t.Fatalf("Failed to initialize metrics: %v", err)
	}
	defer func() {
		if err := mp.Shutdown(ctx); err != nil {
			t.Logf("Failed to shutdown meter provider: %v", err)
		}
	}()

	// Record a metric
	RequestCount.Add(ctx, 5)

	// Test metrics endpoint
	metricServer := http.NewServeMux()
	metricServer.Handle("/metrics", promhttp.Handler())

	req, err := http.NewRequest("GET", "/metrics", nil)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}

	rr := httptest.NewRecorder()
	metricServer.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("Handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	metricsData := rr.Body.String()

	// Look for the metric with its full format including OpenTelemetry labels
	if !strings.Contains(metricsData, `lokxy_request_count_total{otel_scope_name="lokxy",otel_scope_schema_url="",otel_scope_version=""} 5`) {
		t.Errorf("Expected metric not found in output")
		t.Logf("Metrics output:\n%s", metricsData)
	}
}
