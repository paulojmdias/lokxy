//go:build integration

package integration

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// lokxyAddr returns the base URL for lokxy, configurable via LOKXY_ADDR.
func lokxyAddr(t *testing.T) string {
	t.Helper()
	addr := os.Getenv("LOKXY_ADDR")
	if addr == "" {
		addr = "localhost:3100"
	}
	return fmt.Sprintf("http://%s", addr)
}

// lokxyWSAddr returns the WebSocket base URL for lokxy.
func lokxyWSAddr(t *testing.T) string {
	t.Helper()
	addr := os.Getenv("LOKXY_ADDR")
	if addr == "" {
		addr = "localhost:3100"
	}
	return fmt.Sprintf("ws://%s", addr)
}

// nowNano returns the current time as a Unix nanosecond timestamp string.
func nowNano() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

// startNano returns a Unix nanosecond timestamp 1 hour ago.
func startNano() string {
	return fmt.Sprintf("%d", time.Now().Add(-1*time.Hour).UnixNano())
}

// queryParams builds a query string from key-value pairs.
func queryParams(kv ...string) string {
	if len(kv)%2 != 0 {
		panic("queryParams requires an even number of arguments")
	}
	v := url.Values{}
	for i := 0; i < len(kv); i += 2 {
		v.Set(kv[i], kv[i+1])
	}
	return v.Encode()
}

// waitReady polls /ready until lokxy responds 200 or the timeout elapses.
// Called at the start of TestSmoke; the workflow also has its own curl-based
// readiness check, but this guard makes the test self-contained.
func waitReady(t *testing.T, base string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(base + "/ready") //nolint:noctx
		if err == nil && resp.StatusCode == 200 {
			resp.Body.Close()
			return
		}
		if err == nil {
			resp.Body.Close()
		}
		time.Sleep(500 * time.Millisecond)
	}
	require.Fail(t, "lokxy did not become ready within 30 seconds")
}
