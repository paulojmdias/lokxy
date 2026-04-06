package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-kit/log"
	"github.com/stretchr/testify/require"
)

func TestIsGrafanaHealthCheck(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		expected bool
	}{
		{
			name:     "exact health check query",
			query:    "direction=backward&query=vector%281%29%2Bvector%281%29&time=4000000000",
			expected: true,
		},
		{
			name:     "health check query only",
			query:    "query=vector%281%29%2Bvector%281%29",
			expected: true,
		},
		{
			name:     "different query",
			query:    "query=%7Bjob%3D%22varlogs%22%7D",
			expected: false,
		},
		{
			name:     "empty query param",
			query:    "",
			expected: false,
		},
		{
			name:     "no query param at all",
			query:    "time=4000000000&direction=backward",
			expected: false,
		},
		{
			name:     "similar but not exact query",
			query:    "query=vector%281%29",
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/loki/api/v1/query?"+tc.query, nil)
			got := IsGrafanaHealthCheck(req)
			require.Equal(t, tc.expected, got)
		})
	}
}

func TestWriteGrafanaHealthCheckResponse(t *testing.T) {
	logger := log.NewNopLogger()

	t.Run("with health check time parameter (nanoseconds)", func(t *testing.T) {
		// Grafana health check sends time=4000000000 which is
		// time.Unix(4,0).UnixNano() = 4 seconds in nanoseconds.
		req := httptest.NewRequest(http.MethodGet,
			"/loki/api/v1/query?query=vector%281%29%2Bvector%281%29&time=4000000000", nil)
		rec := httptest.NewRecorder()

		WriteGrafanaHealthCheckResponse(rec, req, logger)

		require.Equal(t, http.StatusOK, rec.Code)
		require.Equal(t, "application/json", rec.Header().Get("Content-Type"))

		var resp map[string]any
		err := json.Unmarshal(rec.Body.Bytes(), &resp)
		require.NoError(t, err)

		require.Equal(t, "success", resp["status"])

		data, ok := resp["data"].(map[string]any)
		require.True(t, ok)
		require.Equal(t, "vector", data["resultType"])

		result, ok := data["result"].([]any)
		require.True(t, ok)
		require.Len(t, result, 1, "expected exactly 1 vector entry")

		entry, ok := result[0].(map[string]any)
		require.True(t, ok)

		// metric should be empty
		metric, ok := entry["metric"].(map[string]any)
		require.True(t, ok)
		require.Empty(t, metric)

		// value should be [4.0, "2"] (4000000000 ns = 4 seconds)
		value, ok := entry["value"].([]any)
		require.True(t, ok)
		require.Len(t, value, 2)

		require.Equal(t, float64(4), value[0])
		require.Equal(t, "2", value[1])
	})

	t.Run("without time parameter uses current time", func(t *testing.T) {
		before := time.Now().Unix()
		req := httptest.NewRequest(http.MethodGet,
			"/loki/api/v1/query?query=vector%281%29%2Bvector%281%29", nil)
		rec := httptest.NewRecorder()

		WriteGrafanaHealthCheckResponse(rec, req, logger)
		after := time.Now().Unix()

		require.Equal(t, http.StatusOK, rec.Code)

		var resp map[string]any
		err := json.Unmarshal(rec.Body.Bytes(), &resp)
		require.NoError(t, err)

		data := resp["data"].(map[string]any)
		result := data["result"].([]any)
		entry := result[0].(map[string]any)
		value := entry["value"].([]any)

		ts := int64(value[0].(float64))
		require.GreaterOrEqual(t, ts, before, "timestamp should be >= time before call")
		require.LessOrEqual(t, ts, after, "timestamp should be <= time after call")
		require.Equal(t, "2", value[1])
	})

	t.Run("with Explore-style nanosecond timestamp", func(t *testing.T) {
		// Grafana Explore sends the current time as nanoseconds, e.g.
		// time.Unix(1700000000,0).UnixNano() = 1700000000000000000.
		req := httptest.NewRequest(http.MethodGet,
			"/loki/api/v1/query?query=vector%281%29%2Bvector%281%29&time=1700000000000000000", nil)
		rec := httptest.NewRecorder()

		WriteGrafanaHealthCheckResponse(rec, req, logger)

		var resp map[string]any
		err := json.Unmarshal(rec.Body.Bytes(), &resp)
		require.NoError(t, err)

		data := resp["data"].(map[string]any)
		result := data["result"].([]any)
		entry := result[0].(map[string]any)
		value := entry["value"].([]any)

		// 1700000000000000000 ns = 1700000000 seconds
		require.Equal(t, float64(1700000000), value[0])
		require.Equal(t, "2", value[1])
	})
}
