package proxy

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/go-kit/log"
)

// Realistic response bodies for upstream httptest servers.
const (
	benchLabelsJSON = `{"status":"success","data":["app","cluster","environment","instance","job","namespace","pod","region","service","team"]}`

	benchQueryRangeJSON = `{"status":"success","data":{"resultType":"streams","result":[` +
		`{"stream":{"app":"nginx","env":"prod","region":"us-east-1"},"values":[` +
		`["1700000000000000000","GET /api/v1/users 200 12ms"],` +
		`["1700000001000000000","POST /api/v1/orders 201 45ms"]` +
		`]},` +
		`{"stream":{"app":"api","env":"prod","region":"us-east-1"},"values":[` +
		`["1700000002000000000","INFO starting handler"],` +
		`["1700000003000000000","ERROR db timeout"]` +
		`]}` +
		`],"stats":{"summary":{"bytesProcessedPerSecond":102400,"linesProcessedPerSecond":1000,"totalBytesProcessed":512000,"totalLinesProcessed":5000,"execTime":0.05}}}}`
)

// mkUpstreamServerB is the benchmark equivalent of mkUpstreamServer (which requires
// *testing.T). It registers cleanup via b.Cleanup so callers don't need defer.
func mkUpstreamServerB(b *testing.B, routes map[string]http.HandlerFunc) *httptest.Server {
	b.Helper()
	mux := http.NewServeMux()
	for p, h := range routes {
		mux.HandleFunc(p, h)
		if dec, err := url.PathUnescape(p); err == nil && dec != p {
			mux.HandleFunc(dec, h)
		}
	}
	srv := httptest.NewServer(mux)
	b.Cleanup(srv.Close)
	return srv
}

// benchmarkFanOut is a shared helper for integration fan-out benchmarks.
// It spins up n httptest servers, all serving respBody on path, builds a mux,
// resets the timer, then drives ServeHTTP in a loop.
func benchmarkFanOut(b *testing.B, n int, path, respBody string) {
	b.Helper()
	logger := log.NewNopLogger()

	urls := make([]string, n)
	for i := range n {
		body := respBody // capture for closure
		srv := mkUpstreamServerB(b, map[string]http.HandlerFunc{
			path: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, body)
			},
		})
		urls[i] = srv.URL
	}

	mux := NewServeMux(logger, mkConfig(urls...))

	b.ResetTimer()
	b.ReportAllocs()

	for range b.N {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		mux.ServeHTTP(rr, req)
	}
}

func BenchmarkProxy_Labels_FanOut(b *testing.B) {
	for _, tc := range []struct {
		name string
		n    int
	}{
		{"1backend", 1},
		{"2backends", 2},
		{"5backends", 5},
	} {
		b.Run(tc.name, func(b *testing.B) {
			benchmarkFanOut(b, tc.n, "/loki/api/v1/labels", benchLabelsJSON)
		})
	}
}

func BenchmarkProxy_QueryRange_Streams_FanOut(b *testing.B) {
	for _, tc := range []struct {
		name string
		n    int
	}{
		{"1backend", 1},
		{"2backends", 2},
		{"5backends", 5},
	} {
		b.Run(tc.name, func(b *testing.B) {
			benchmarkFanOut(b, tc.n, "/loki/api/v1/query_range", benchQueryRangeJSON)
		})
	}
}

func BenchmarkCustomRoundTripper_PlainBody(b *testing.B) {
	body := []byte(`{"status":"success","data":["app","job","region","cluster","namespace"]}`)
	inner := roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{},
			Body:       io.NopCloser(bytes.NewReader(body)),
		}, nil
	})
	rt := &CustomRoundTripper{rt: inner, logger: log.NewNopLogger()}
	req := httptest.NewRequest(http.MethodGet, "/loki/api/v1/labels", nil)

	b.ReportAllocs()

	for b.Loop() {
		resp, err := rt.RoundTrip(req)
		if err != nil {
			b.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
}

func BenchmarkCustomRoundTripper_GzipBody(b *testing.B) {
	plain := []byte(`{"status":"success","data":["app","job","region","cluster","namespace","instance","environment","service","pod","team"]}`)
	gzBytes := mkGzip(plain)

	inner := roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Encoding": []string{"gzip"}},
			Body:       io.NopCloser(bytes.NewReader(gzBytes)),
		}, nil
	})
	rt := &CustomRoundTripper{rt: inner, logger: log.NewNopLogger()}
	req := httptest.NewRequest(http.MethodGet, "/loki/api/v1/labels", nil)

	b.ReportAllocs()

	for b.Loop() {
		resp, err := rt.RoundTrip(req)
		if err != nil {
			b.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
}

func BenchmarkProxy_Labels_ConnectionReuse(b *testing.B) {
	// Measures fan-out performance where connection pool tuning matters.
	// With proper pool settings, connections are reused across iterations.
	for _, tc := range []struct {
		name string
		n    int
	}{
		{"1backend", 1},
		{"2backends", 2},
		{"5backends", 5},
	} {
		b.Run(tc.name, func(b *testing.B) {
			benchmarkFanOut(b, tc.n, "/loki/api/v1/labels", benchLabelsJSON)
		})
	}
}

func BenchmarkProxy_QueryRange_ConnectionReuse(b *testing.B) {
	for _, tc := range []struct {
		name string
		n    int
	}{
		{"1backend", 1},
		{"2backends", 2},
		{"5backends", 5},
	} {
		b.Run(tc.name, func(b *testing.B) {
			benchmarkFanOut(b, tc.n, "/loki/api/v1/query_range", benchQueryRangeJSON)
		})
	}
}
