package proxy

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/go-kit/log"
	"github.com/stretchr/testify/require"
)

func TestIsLogvolhistQuery(t *testing.T) {
	tests := []struct {
		name     string
		header   string
		expected bool
	}{
		{
			name:     "exact grafana header",
			header:   "Source=logvolhist",
			expected: true,
		},
		{
			name:     "multiple tags",
			header:   "Source=logvolhist,Feature=explore",
			expected: true,
		},
		{
			name:     "no header",
			header:   "",
			expected: false,
		},
		{
			name:     "different source",
			header:   "Source=dashboard",
			expected: false,
		},
		{
			name:     "logvolhist substring in value",
			header:   "logvolhist_custom",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/loki/api/v1/query_range", nil)
			if tt.header != "" {
				req.Header.Set("X-Query-Tags", tt.header)
			}
			require.Equal(t, tt.expected, isLogvolhistQuery(req))
		})
	}
}

func TestRewriteLogvolhistRequest(t *testing.T) {
	logger := log.NewNopLogger()

	tests := []struct {
		name     string
		query    string
		step     string
		cfgStep  string
		wantStep string
	}{
		{
			name:     "rewrites both query and step",
			query:    `sum by (level) (count_over_time({app="foo"}[5s]))`,
			step:     "5",
			cfgStep:  "1m",
			wantStep: "1m",
		},
		{
			name:     "no rewrite when interval already large enough",
			query:    `count_over_time({app="foo"}[5m])`,
			step:     "300",
			cfgStep:  "1m",
			wantStep: "1m", // step is always forced for logvolhist
		},
		{
			name:     "empty query passes through",
			query:    "",
			step:     "5",
			cfgStep:  "1m",
			wantStep: "5", // no rewrite when query is empty
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := mkConfig("http://localhost:3100")
			config.API.QueryRange.Step = tt.cfgStep

			reqURL := "/loki/api/v1/query_range?step=" + tt.step + "&start=1000&end=2000"
			if tt.query != "" {
				reqURL += "&query=" + url.QueryEscape(tt.query)
			}
			req := httptest.NewRequest(http.MethodGet, reqURL, nil)
			req.Header.Set("X-Query-Tags", "Source=logvolhist")

			got := rewriteLogvolhistRequest(req, config, logger)
			require.Equal(t, tt.wantStep, got.URL.Query().Get("step"))
			// start and end should be preserved
			require.Equal(t, "1000", got.URL.Query().Get("start"))
			require.Equal(t, "2000", got.URL.Query().Get("end"))
		})
	}
}

func TestRewriteLogvolhistRequest_InvalidLogQL_FailsOpen(t *testing.T) {
	logger := log.NewNopLogger()
	config := mkConfig("http://localhost:3100")
	config.API.QueryRange.Step = "1m"

	req := httptest.NewRequest(http.MethodGet,
		"/loki/api/v1/query_range?query="+url.QueryEscape(`{{{invalid`)+"&step=5", nil)
	req.Header.Set("X-Query-Tags", "Source=logvolhist")

	got := rewriteLogvolhistRequest(req, config, logger)
	// Should return request unmodified on parse error
	require.Equal(t, "5", got.URL.Query().Get("step"))
}

func TestRewriteLogvolhistRequest_NoStepConfig_PassesThrough(t *testing.T) {
	logger := log.NewNopLogger()
	config := mkConfig("http://localhost:3100")
	// No step configured
	config.API.QueryRange.Step = ""

	req := httptest.NewRequest(http.MethodGet,
		"/loki/api/v1/query_range?query="+url.QueryEscape(`count_over_time({app="foo"}[5s])`)+"&step=5", nil)
	req.Header.Set("X-Query-Tags", "Source=logvolhist")

	got := rewriteLogvolhistRequest(req, config, logger)
	// Should not modify the request when no step is configured
	require.Equal(t, "5", got.URL.Query().Get("step"))
}

func TestRewriteLogvolhistRequest_VerifiesQueryRewrite(t *testing.T) {
	logger := log.NewNopLogger()
	config := mkConfig("http://localhost:3100")
	config.API.QueryRange.Step = "1m"

	originalQuery := `sum by (level, detected_level) (count_over_time({app="foo"}[5s]))`
	req := httptest.NewRequest(http.MethodGet,
		"/loki/api/v1/query_range?query="+url.QueryEscape(originalQuery)+"&step=5&start=1000&end=2000", nil)
	req.Header.Set("X-Query-Tags", "Source=logvolhist")

	got := rewriteLogvolhistRequest(req, config, logger)
	rewrittenQuery := got.URL.Query().Get("query")
	// The query should have the range vector rewritten to 1m
	require.Contains(t, rewrittenQuery, "[1m]")
	require.NotContains(t, rewrittenQuery, "[5s]")
	// Step should also be set to 1m
	require.Equal(t, "1m", got.URL.Query().Get("step"))
}

// -- Integration tests --

func TestLogvolhist_Integration_QueryRangeRewrite(t *testing.T) {
	var receivedStep string
	var receivedQuery string

	upstream := mkUpstreamServer(t, map[string]http.HandlerFunc{
		"/loki/api/v1/query_range": func(w http.ResponseWriter, r *http.Request) {
			receivedStep = r.URL.Query().Get("step")
			receivedQuery = r.URL.Query().Get("query")
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"status":"success","data":{"resultType":"matrix","result":[],"stats":{}}}`)
		},
	})
	defer upstream.Close()

	config := mkConfig(upstream.URL)
	config.API.QueryRange.Step = "1m"
	config.API.Logvolhist.Enabled = true

	logger := log.NewNopLogger()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet,
		"/loki/api/v1/query_range?query="+url.QueryEscape(
			`sum by (level) (count_over_time({app="foo"}[5s]))`)+
			"&step=5&start=1000&end=2000",
		nil)
	req.Header.Set("X-Query-Tags", "Source=logvolhist")

	NewServeMux(logger, config).ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "1m", receivedStep, "step should be rewritten to configured step")
	require.Contains(t, receivedQuery, "[1m]", "range vector should be rewritten to 1m")
	require.NotContains(t, receivedQuery, "[5s]", "original 5s interval should be replaced")
}

func TestLogvolhist_Integration_NonLogvolhistQueryUnchanged(t *testing.T) {
	var receivedStep string
	var receivedQuery string

	upstream := mkUpstreamServer(t, map[string]http.HandlerFunc{
		"/loki/api/v1/query_range": func(w http.ResponseWriter, r *http.Request) {
			receivedStep = r.URL.Query().Get("step")
			receivedQuery = r.URL.Query().Get("query")
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"status":"success","data":{"resultType":"matrix","result":[],"stats":{}}}`)
		},
	})
	defer upstream.Close()

	config := mkConfig(upstream.URL)
	config.API.QueryRange.Step = "1m"
	config.API.Logvolhist.Enabled = true

	logger := log.NewNopLogger()
	rr := httptest.NewRecorder()
	originalQuery := `sum by (level) (count_over_time({app="foo"}[5s]))`
	req := httptest.NewRequest(http.MethodGet,
		"/loki/api/v1/query_range?query="+url.QueryEscape(originalQuery)+
			"&step=5&start=1000&end=2000",
		nil)
	// No X-Query-Tags header -- this is a regular query, not logvolhist

	NewServeMux(logger, config).ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	// Step should still be overridden by query_range.step (existing behavior)
	require.Equal(t, "1m", receivedStep)
	// But the query should NOT be rewritten (no logvolhist detection)
	require.Contains(t, receivedQuery, "[5s]", "non-logvolhist query should not have range vector rewritten")
}

func TestLogvolhist_Integration_DisabledConfig_NoRewrite(t *testing.T) {
	var receivedQuery string

	upstream := mkUpstreamServer(t, map[string]http.HandlerFunc{
		"/loki/api/v1/query_range": func(w http.ResponseWriter, r *http.Request) {
			receivedQuery = r.URL.Query().Get("query")
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"status":"success","data":{"resultType":"matrix","result":[],"stats":{}}}`)
		},
	})
	defer upstream.Close()

	config := mkConfig(upstream.URL)
	config.API.QueryRange.Step = "1m"
	config.API.Logvolhist.Enabled = false // disabled

	logger := log.NewNopLogger()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet,
		"/loki/api/v1/query_range?query="+url.QueryEscape(
			`sum by (level) (count_over_time({app="foo"}[5s]))`)+
			"&step=5&start=1000&end=2000",
		nil)
	req.Header.Set("X-Query-Tags", "Source=logvolhist")

	NewServeMux(logger, config).ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	// Query should NOT be rewritten when logvolhist is disabled
	require.Contains(t, receivedQuery, "[5s]")
}

func TestLogvolhistTimeout_Applied(t *testing.T) {
	// Upstream that sleeps longer than the logvolhist timeout.
	slow := mkUpstreamServer(t, map[string]http.HandlerFunc{
		"/loki/api/v1/query_range": func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-r.Context().Done():
				// Context was cancelled by timeout
				http.Error(w, "timeout", http.StatusGatewayTimeout)
				return
			case <-time.After(5 * time.Second):
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, `{"status":"success","data":{"resultType":"matrix","result":[],"stats":{}}}`)
			}
		},
	})
	defer slow.Close()

	config := mkConfig(slow.URL)
	config.API.QueryRange.Step = "1m"
	config.API.Logvolhist.Enabled = true
	config.API.Logvolhist.Timeout = "1s" // 1 second timeout

	logger := log.NewNopLogger()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet,
		"/loki/api/v1/query_range?query="+url.QueryEscape(`count_over_time({app="foo"}[5s])`)+"&step=5&start=1000&end=2000",
		nil)
	req.Header.Set("X-Query-Tags", "Source=logvolhist")

	start := time.Now()
	NewServeMux(logger, config).ServeHTTP(rr, req)
	elapsed := time.Since(start)

	// Should complete in ~1s due to timeout, not 5s
	require.Less(t, elapsed, 3*time.Second, "request should have been cancelled by logvolhist timeout")
}

func TestLogvolhistTimeout_NotApplied_WhenNotConfigured(t *testing.T) {
	fast := mkUpstreamServer(t, map[string]http.HandlerFunc{
		"/loki/api/v1/query_range": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"status":"success","data":{"resultType":"matrix","result":[],"stats":{}}}`)
		},
	})
	defer fast.Close()

	config := mkConfig(fast.URL)
	config.API.QueryRange.Step = "1m"
	config.API.Logvolhist.Enabled = true
	config.API.Logvolhist.Timeout = "" // no timeout

	logger := log.NewNopLogger()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet,
		"/loki/api/v1/query_range?query="+url.QueryEscape(`count_over_time({app="foo"}[5s])`)+"&step=5&start=1000&end=2000",
		nil)
	req.Header.Set("X-Query-Tags", "Source=logvolhist")

	NewServeMux(logger, config).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
}

// -- LVH-07: Metrics smoke test --

func TestLogvolhist_Integration_MetricsSmoke_Rewrite(t *testing.T) {
	// Verifies the logvolhist handler completes successfully when metrics are
	// recorded. The noop metric defaults are used in tests (no OTel provider
	// initialized), so this confirms metrics.Add/Record calls don't panic.
	upstream := mkUpstreamServer(t, map[string]http.HandlerFunc{
		"/loki/api/v1/query_range": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"status":"success","data":{"resultType":"matrix","result":[],"stats":{}}}`)
		},
	})
	defer upstream.Close()

	config := mkConfig(upstream.URL)
	config.API.QueryRange.Step = "1m"
	config.API.Logvolhist.Enabled = true

	logger := log.NewNopLogger()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet,
		"/loki/api/v1/query_range?query="+url.QueryEscape(
			`count_over_time({app="foo"} |= "error" [5s])`)+
			"&step=5&start=1000&end=2000",
		nil)
	req.Header.Set("X-Query-Tags", "Source=logvolhist")

	NewServeMux(logger, config).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
}

// -- Response aggregation tests --

func TestLogvolhist_Integration_AggregationWhenGrafanaStepLarger(t *testing.T) {
	// Upstream returns 6 data points at 1m intervals (millisecond timestamps).
	// Grafana asks step=180 (3 minutes), config step=1m.
	// Aggregation should sum into 2 x 3m buckets.
	upstream := mkUpstreamServer(t, map[string]http.HandlerFunc{
		"/loki/api/v1/query_range": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{
				"status": "success",
				"data": {
					"resultType": "matrix",
					"result": [{
						"metric": {"level": "info"},
						"values": [
							[0,   "10"],
							[60,  "20"],
							[120, "30"],
							[180, "40"],
							[240, "50"],
							[300, "60"]
						]
					}],
					"stats": {}
				}
			}`)
		},
	})
	defer upstream.Close()

	config := mkConfig(upstream.URL)
	config.API.QueryRange.Step = "1m"
	config.API.Logvolhist.Enabled = true

	logger := log.NewNopLogger()
	rr := httptest.NewRecorder()
	// Grafana sends step=180 (3 minutes in seconds) -- larger than config step (1m)
	req2 := httptest.NewRequest(http.MethodGet,
		"/loki/api/v1/query_range?query="+url.QueryEscape(
			`sum by (level) (count_over_time({app="foo"}[5s]))`)+
			"&step=180&start=0&end=360000",
		nil)
	req2.Header.Set("X-Query-Tags", "Source=logvolhist")

	NewServeMux(logger, config).ServeHTTP(rr, req2)

	require.Equal(t, http.StatusOK, rr.Code)

	var response map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &response))

	data := response["data"].(map[string]any)
	require.Equal(t, "matrix", data["resultType"])

	result := data["result"].([]any)
	require.Len(t, result, 1)

	values := result[0].(map[string]any)["values"].([]any)
	// 6 one-minute points aggregated into 3-minute buckets -> 2 buckets
	require.Len(t, values, 2, "should aggregate 6 x 1m points into 2 x 3m buckets")

	// Bucket 0: 10+20+30=60
	v0, err := strconv.ParseFloat(values[0].([]any)[1].(string), 64)
	require.NoError(t, err)
	require.InDelta(t, 60.0, v0, 0.01)
	// Bucket 1: 40+50+60=150
	v1, err := strconv.ParseFloat(values[1].([]any)[1].(string), 64)
	require.NoError(t, err)
	require.InDelta(t, 150.0, v1, 0.01)
}

func TestLogvolhist_Integration_NoAggregationWhenGrafanaStepSmaller(t *testing.T) {
	// Upstream returns 3 data points. Grafana asks step=5 (5s), config step=1m.
	// Since 5s < 1m, no aggregation should occur -- all points pass through.
	upstream := mkUpstreamServer(t, map[string]http.HandlerFunc{
		"/loki/api/v1/query_range": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{
				"status": "success",
				"data": {
					"resultType": "matrix",
					"result": [{
						"metric": {"level": "info"},
						"values": [
							[0,   "10"],
							[60,  "20"],
							[120, "30"]
						]
					}],
					"stats": {}
				}
			}`)
		},
	})
	defer upstream.Close()

	config := mkConfig(upstream.URL)
	config.API.QueryRange.Step = "1m"
	config.API.Logvolhist.Enabled = true

	logger := log.NewNopLogger()
	rr2 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodGet,
		"/loki/api/v1/query_range?query="+url.QueryEscape(
			`count_over_time({app="foo"}[5s])`)+
			"&step=5&start=0&end=180000",
		nil)
	req3.Header.Set("X-Query-Tags", "Source=logvolhist")

	NewServeMux(logger, config).ServeHTTP(rr2, req3)

	require.Equal(t, http.StatusOK, rr2.Code)

	var response2 map[string]any
	require.NoError(t, json.Unmarshal(rr2.Body.Bytes(), &response2))

	data2 := response2["data"].(map[string]any)
	result2 := data2["result"].([]any)
	require.Len(t, result2, 1)

	values2 := result2[0].(map[string]any)["values"].([]any)
	// No aggregation: all 3 original points preserved
	require.Len(t, values2, 3, "no aggregation when grafana step < config step")
}
