//go:build integration

// Package integration contains smoke tests that drive real HTTP and WebSocket
// traffic through a running lokxy instance. These tests are used in CI alongside
// weaver live-check to validate that lokxy emits well-formed OTLP telemetry.
//
// Run with:
//
//	LOKXY_ADDR=localhost:3100 go test -v -tags=integration -timeout=120s ./test/integration/
package integration

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

// lokxyGet issues a GET request and returns the response. It does not fail the
// test on non-2xx status codes because some endpoints return errors from fresh
// Loki instances with no data -- the span shape is still valid.
func lokxyGet(t *testing.T, url string) (*http.Response, error) {
	t.Helper()
	resp, err := http.Get(url) //nolint:noctx
	if err != nil {
		return nil, err
	}
	resp.Body.Close()
	return resp, nil
}

// TestSmoke is the single entry point. Each sub-test covers one proxy route type.
// The goal is to generate all span types so weaver live-check can assess them.
func TestSmoke(t *testing.T) {
	base := lokxyAddr(t)
	wsBase := lokxyWSAddr(t)
	waitReady(t, base)

	// api_route -- standard fanout endpoints (return 200 with empty data on a
	// fresh Loki instance; non-2xx would still generate valid spans)
	t.Run("Labels", func(t *testing.T) {
		_, err := lokxyGet(t, base+"/loki/api/v1/labels")
		require.NoError(t, err)
	})

	t.Run("QueryRange", func(t *testing.T) {
		q := queryParams(
			"query", `{job="test"}`,
			"start", startNano(),
			"end", nowNano(),
			"step", "60",
		)
		_, err := lokxyGet(t, base+"/loki/api/v1/query_range?"+q)
		require.NoError(t, err)
	})

	t.Run("Query", func(t *testing.T) {
		q := queryParams("query", `{job="test"}`, "time", nowNano())
		_, err := lokxyGet(t, base+"/loki/api/v1/query?"+q)
		require.NoError(t, err)
	})

	// health_check_intercept -- Grafana sends this exact query; lokxy short-circuits it
	t.Run("GrafanaHealthCheck", func(t *testing.T) {
		q := queryParams("query", "vector(1)+vector(1)")
		resp, err := lokxyGet(t, base+"/loki/api/v1/query?"+q)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("Series", func(t *testing.T) {
		q := queryParams(
			"match[]", `{job="test"}`,
			"start", startNano(),
			"end", nowNano(),
		)
		_, err := lokxyGet(t, base+"/loki/api/v1/series?"+q)
		require.NoError(t, err)
	})

	t.Run("IndexStats", func(t *testing.T) {
		q := queryParams(
			"query", `{job="test"}`,
			"start", startNano(),
			"end", nowNano(),
		)
		_, err := lokxyGet(t, base+"/loki/api/v1/index/stats?"+q)
		require.NoError(t, err)
	})

	t.Run("IndexVolume", func(t *testing.T) {
		q := queryParams(
			"query", `{job="test"}`,
			"start", startNano(),
			"end", nowNano(),
		)
		_, err := lokxyGet(t, base+"/loki/api/v1/index/volume?"+q)
		require.NoError(t, err)
	})

	t.Run("IndexVolumeRange", func(t *testing.T) {
		q := queryParams(
			"query", `{job="test"}`,
			"start", startNano(),
			"end", nowNano(),
			"step", "60",
		)
		_, err := lokxyGet(t, base+"/loki/api/v1/index/volume_range?"+q)
		require.NoError(t, err)
	})

	t.Run("DetectedLabels", func(t *testing.T) {
		q := queryParams(
			"query", `{job="test"}`,
			"start", startNano(),
			"end", nowNano(),
		)
		_, err := lokxyGet(t, base+"/loki/api/v1/detected_labels?"+q)
		require.NoError(t, err)
	})

	t.Run("Patterns", func(t *testing.T) {
		q := queryParams(
			"query", `{job="test"}`,
			"start", startNano(),
			"end", nowNano(),
		)
		_, err := lokxyGet(t, base+"/loki/api/v1/patterns?"+q)
		require.NoError(t, err)
	})

	t.Run("DetectedFields", func(t *testing.T) {
		q := queryParams(
			"query", `{job="test"}`,
			"start", startNano(),
			"end", nowNano(),
		)
		_, err := lokxyGet(t, base+"/loki/api/v1/detected_fields?"+q)
		require.NoError(t, err)
	})

	// label_values route type
	t.Run("LabelValues", func(t *testing.T) {
		q := queryParams("start", startNano(), "end", nowNano())
		_, err := lokxyGet(t, base+"/loki/api/v1/label/job/values?"+q)
		require.NoError(t, err)
	})

	// detected_field_values route type
	t.Run("DetectedFieldValues", func(t *testing.T) {
		q := queryParams(
			"query", `{job="test"}`,
			"start", startNano(),
			"end", nowNano(),
		)
		_, err := lokxyGet(t, base+"/loki/api/v1/detected_field/level/values?"+q)
		require.NoError(t, err)
	})

	// first_response route type -- POST push endpoint and unknown-path fallback
	t.Run("Push", func(t *testing.T) {
		now := nowNano()
		body := `{"streams":[{"stream":{"job":"lokxy-smoke"},"values":[["` + now + `","smoke test log line"]]}]}`
		resp, err := http.Post( //nolint:noctx
			base+"/loki/api/v1/push",
			"application/json",
			strings.NewReader(body),
		)
		if err == nil {
			resp.Body.Close()
		}
		// Error is acceptable -- we just need the span to be emitted.
	})

	t.Run("FallbackRoute", func(t *testing.T) {
		_, _ = lokxyGet(t, base+"/unknown-path-for-fallback")
		// first_response route; result is irrelevant.
	})

	// websocket route type -- dial, receive one frame or wait for close, disconnect
	t.Run("WebSocketTail", func(t *testing.T) {
		tailURL := wsBase + "/loki/api/v1/tail?query=" + url.QueryEscape(`{job="lokxy-smoke"}`)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		conn, _, err := websocket.DefaultDialer.DialContext(ctx, tailURL, nil)
		if err != nil {
			// lokxy may return a non-upgrade response if Loki is not ready;
			// the important thing is that the HTTP span was emitted for the attempt.
			t.Logf("WebSocket dial returned: %v (span still emitted)", err)
			return
		}
		defer conn.Close()

		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		var msg map[string]any
		_ = conn.ReadJSON(&msg) // timeout or close is fine; span is emitted on dial
	})
}
