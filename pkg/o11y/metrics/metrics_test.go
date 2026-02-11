package metrics //nolint:revive

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInitMetrics(t *testing.T) {
	ctx := t.Context()
	mp, err := Initialize(ctx)
	require.NoError(t, err)

	t.Cleanup(func() {
		if err := mp.Shutdown(ctx); err != nil {
			t.Logf("Failed to shutdown meter provider: %v", err)
		}
	})

	// Record a metric
	RequestCount.Add(ctx, 5)

	// Test metrics endpoint
	metricServer := NewServeMux()

	req, err := http.NewRequest("GET", "/metrics", nil)
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	metricServer.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)

	metricsData := rr.Body.String()

	// Look for the metric with its full format including OpenTelemetry labels
	require.Contains(t, metricsData, `lokxy_request_count_total{otel_scope_name="lokxy",otel_scope_schema_url="",otel_scope_version=""} 5`)
}
