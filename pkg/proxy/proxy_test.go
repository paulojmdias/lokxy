package proxy

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/stretchr/testify/require"

	cfg "github.com/paulojmdias/lokxy/pkg/config"
	"github.com/paulojmdias/lokxy/pkg/proxy/proxyresponse"
)

// ---------- helpers ----------

func mkGzip(body []byte) []byte {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write(body)
	_ = zw.Close()
	return buf.Bytes()
}

func mkUpstreamServer(t *testing.T, routes map[string]http.HandlerFunc) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	for p, h := range routes {
		// Register the given path
		mux.HandleFunc(p, h)
		// Also register decoded variant if different
		if dec, err := url.PathUnescape(p); err == nil && dec != p {
			mux.HandleFunc(dec, h)
		}
	}
	return httptest.NewServer(mux)
}

func mkConfig(urls ...string) *cfg.Config {
	sgs := make([]cfg.ServerGroup, 0, len(urls))
	for i, u := range urls {
		hc := cfg.HTTPClientConfig{}
		hc.DialTimeout = 0
		hc.TLSConfig.InsecureSkipVerify = true

		sgs = append(sgs, cfg.ServerGroup{
			Name:    "sg" + strconv.Itoa(i+1),
			URL:     u,
			Timeout: 2,
			Headers: map[string]string{
				"X-Lokxy": "test",
			},
			HTTPClientConfig: hc,
		})
	}
	return &cfg.Config{ServerGroups: sgs}
}

// ---------- tests ----------

func TestProxy_ApiRoute_FanOutAndAggregateHook(t *testing.T) {
	logger := log.NewNopLogger()

	s1 := mkUpstreamServer(t, map[string]http.HandlerFunc{
		"/loki/api/v1/labels": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"status": "success", "data":["a", "b"]}`)
		},
	})
	defer s1.Close()

	s2 := mkUpstreamServer(t, map[string]http.HandlerFunc{
		"/loki/api/v1/labels": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"status": "success", "data":["c", "b"]}`)
		},
	})
	defer s2.Close()

	config := mkConfig(s1.URL, s2.URL)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/loki/api/v1/labels", nil)

	NewServeMux(logger, config).ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)

	var got map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	require.Equal(t, "success", got["status"])
	require.ElementsMatch(t, []any{"a", "b", "c"}, got["data"])
}

func TestProxy_DetectedFieldValues_PathExtractionAndMerge(t *testing.T) {
	logger := log.NewNopLogger()
	encoded := url.PathEscape("foo/bar")
	upPath := "/loki/api/v1/detected_field/" + encoded + "/values"

	s1 := mkUpstreamServer(t, map[string]http.HandlerFunc{
		upPath: func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"field":"ignored","values":[{"value":"X","count":1},{"value":"Y","count":2}]}`)
		},
	})
	defer s1.Close()

	s2 := mkUpstreamServer(t, map[string]http.HandlerFunc{
		upPath: func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"field":"ignored","values":[{"value":"X","count":3}]}`)
		},
	})
	defer s2.Close()

	config := mkConfig(s1.URL, s2.URL)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/loki/api/v1/detected_field/"+encoded+"/values", nil)
	q := req.URL.Query()
	q.Set("query", `{app="lokxy"}`)
	req.URL.RawQuery = q.Encode()

	NewServeMux(logger, config).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	var out struct {
		Field  string `json:"field"`
		Values []struct {
			Value string `json:"value"`
			Count int    `json:"count"`
		} `json:"values"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &out))

	require.Equal(t, "foo/bar", out.Field)
	values := map[string]int{}
	for _, v := range out.Values {
		values[v.Value] = v.Count
	}
	require.Equal(t, 4, values["X"])
	require.Equal(t, 2, values["Y"])
}

func TestProxy_UnknownPath_ForwardsFirstResponseWithGzipBody(t *testing.T) {
	logger := log.NewNopLogger()

	plain := []byte(`{"hello":"world"}`)
	gz := mkGzip(plain)

	s1 := mkUpstreamServer(t, map[string]http.HandlerFunc{
		"/unknown": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Encoding", "gzip")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(gz)
		},
	})
	defer s1.Close()

	config := mkConfig(s1.URL)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/unknown", nil)

	NewServeMux(logger, config).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	require.JSONEq(t, string(plain), rr.Body.String())
}

func TestProxy_FanOut_POSTBodyReused(t *testing.T) {
	logger := log.NewNopLogger()

	var got1, got2 string
	up := "/loki/api/v1/query"
	s1 := mkUpstreamServer(t, map[string]http.HandlerFunc{
		up: func(w http.ResponseWriter, r *http.Request) {
			defer r.Body.Close()
			b, _ := io.ReadAll(r.Body)
			got1 = string(b)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"streams","result":[],"stats":{}}}`))
		},
	})
	defer s1.Close()
	s2 := mkUpstreamServer(t, map[string]http.HandlerFunc{
		up: func(w http.ResponseWriter, r *http.Request) {
			defer r.Body.Close()
			b, _ := io.ReadAll(r.Body)
			got2 = string(b)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"streams","result":[],"stats":{}}}`))
		},
	})
	defer s2.Close()

	config := mkConfig(s1.URL, s2.URL)

	rr := httptest.NewRecorder()
	body := bytes.NewBufferString(`query={app="lokxy"}`)
	req := httptest.NewRequest(http.MethodPost, up, body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	NewServeMux(logger, config).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	require.Equal(t, `query={app="lokxy"}`, got1)
	require.Equal(t, `query={app="lokxy"}`, got2)
}

func TestProxy_QueryRange_SlowStreamingBody_NotCanceledBeforeMerge(t *testing.T) {
	logger := log.NewNopLogger()

	up := "/loki/api/v1/query_range"

	var slowCanceled atomic.Bool
	var slowFinalWriteErr atomic.Bool
	s1 := mkUpstreamServer(t, map[string]http.HandlerFunc{
		up: func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"status":"success","data":{"resultType":"streams","result":[`)
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}

			select {
			case <-r.Context().Done():
				slowCanceled.Store(true)
				return
			case <-time.After(150 * time.Millisecond):
			}

			if _, err := io.WriteString(w, `{"stream":{"app":"lokxy","src":"s1"},"values":[["1609459200000000000","line-1"]]}],"stats":{}}}`); err != nil {
				slowFinalWriteErr.Store(true)
			}
		},
	})
	defer s1.Close()

	s2 := mkUpstreamServer(t, map[string]http.HandlerFunc{
		up: func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"status":"success","data":{"resultType":"streams","result":[{"stream":{"app":"lokxy","src":"s2"},"values":[["1609459200000000000","line-2"]]}],"stats":{}}}`)
		},
	})
	defer s2.Close()

	cfg := mkConfig(s1.URL, s2.URL)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, up+"?query=%7Bapp%3D%22lokxy%22%7D", nil)

	NewServeMux(logger, cfg).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	require.False(t, slowCanceled.Load(), "slow upstream request context was canceled before body was fully read")
	require.False(t, slowFinalWriteErr.Load(), "slow upstream could not finish writing the response body")

	var out map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &out))
	require.Equal(t, "success", out["status"])

	data, ok := out["data"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "streams", data["resultType"])

	result, ok := data["result"].([]any)
	require.True(t, ok)
	require.Len(t, result, 2)
}

func TestProxy_UpstreamHeadersInjected(t *testing.T) {
	logger := log.NewNopLogger()

	var seen string
	up := "/loki/api/v1/labels"
	s1 := mkUpstreamServer(t, map[string]http.HandlerFunc{
		up: func(w http.ResponseWriter, r *http.Request) {
			seen = r.Header.Get("X-Lokxy")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"labels":[]}`))
		},
	})
	defer s1.Close()

	cfg := mkConfig(s1.URL)
	// override to prove it’s our injection (not client header).
	cfg.ServerGroups[0].Headers["X-Lokxy"] = "from-config"

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, up, nil)
	req.Header.Set("X-Lokxy", "from-client") // should be overwritten by config

	NewServeMux(logger, cfg).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "from-config", seen)
}

func TestProxy_DetectedFieldValues_UpstreamFailure(t *testing.T) {
	logger := log.NewNopLogger()
	encoded := url.PathEscape("foo")
	upPath := "/loki/api/v1/detected_field/" + encoded + "/values"

	errorBody := "upstream error"

	// Broken upstream - any backend failure should cause the request to fail
	s1 := mkUpstreamServer(t, map[string]http.HandlerFunc{
		upPath: func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			io.WriteString(w, errorBody)
		},
	})
	defer s1.Close()

	config := mkConfig(s1.URL)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/loki/api/v1/detected_field/"+encoded+"/values", nil)

	NewServeMux(logger, config).ServeHTTP(rr, req)

	// Should return error when backend fails (fail-fast behavior)
	require.Equal(t, http.StatusInternalServerError, rr.Code)
	require.Equal(t, "text/plain; charset=utf-8", rr.Header().Get("Content-Type"))
	require.Equal(t, "sg1", rr.Header().Get("Failed-Backend"))
	require.Contains(t, rr.Body.String(), errorBody)
}

func TestProxy_ApiRoutes_Dispatch(t *testing.T) {
	logger := log.NewNopLogger()

	// upstream presence isn’t needed; handler ignores result bodies.
	s := mkUpstreamServer(t, map[string]http.HandlerFunc{
		"/loki/api/v1/series": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"status":"success","data":[]}`)
		},
	})
	defer s.Close()

	cfg := mkConfig(s.URL)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/loki/api/v1/series", nil)

	NewServeMux(logger, cfg).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	var got map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	require.Equal(t, "success", got["status"])
}

func TestProxy_AllBackendsFailWithError(t *testing.T) {
	logger := log.NewNopLogger()

	errorBody := `{"status":"error","error":"parse error"}`

	s1 := mkUpstreamServer(t, map[string]http.HandlerFunc{
		"/loki/api/v1/query": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, errorBody)
		},
	})
	defer s1.Close()

	s2 := mkUpstreamServer(t, map[string]http.HandlerFunc{
		"/loki/api/v1/query": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, errorBody)
		},
	})
	defer s2.Close()

	cfg := mkConfig(s1.URL, s2.URL)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/loki/api/v1/query?query={}", nil)

	NewServeMux(logger, cfg).ServeHTTP(rr, req)

	// Should return error status from first backend
	require.Equal(t, http.StatusBadRequest, rr.Code)
	// Should use simple text format: {backend}: {error}
	responseBody := rr.Body.String()
	// Either sg1 or sg2 could respond first (race condition)
	require.True(t, strings.HasPrefix(responseBody, "sg1:") || strings.HasPrefix(responseBody, "sg2:"),
		"Response should start with backend name (got: %s)", responseBody)
	require.Contains(t, responseBody, errorBody)
	// Should be plain text, not JSON
	require.Equal(t, "text/plain; charset=utf-8", rr.Header().Get("Content-Type"))
	// Should have Failed-Backend header (either sg1 or sg2)
	failedBackend := rr.Header().Get("Failed-Backend")
	require.True(t, failedBackend == "sg1" || failedBackend == "sg2",
		"Failed-Backend header should be sg1 or sg2 (got: %s)", failedBackend)
}

func TestProxy_AnyBackendFailure_ReturnsError(t *testing.T) {
	logger := log.NewNopLogger()

	errorBody := "backend error from sg2"

	// Only failing backend - to ensure deterministic behavior
	s1 := mkUpstreamServer(t, map[string]http.HandlerFunc{
		"/loki/api/v1/labels": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			io.WriteString(w, errorBody)
		},
	})
	defer s1.Close()

	cfg := mkConfig(s1.URL)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/loki/api/v1/labels", nil)

	NewServeMux(logger, cfg).ServeHTTP(rr, req)

	// Should return error when backend fails
	require.Equal(t, http.StatusInternalServerError, rr.Code)
	require.Equal(t, "text/plain; charset=utf-8", rr.Header().Get("Content-Type"))
	require.Equal(t, "sg1", rr.Header().Get("Failed-Backend"))
	require.Contains(t, rr.Body.String(), errorBody)
}

func TestProxy_UnreachableBackend_ReturnsConnectionError(t *testing.T) {
	logger := log.NewNopLogger()

	// Use an invalid URL that will fail to connect
	cfg := mkConfig("http://127.0.0.1:1") // Port 1 is unlikely to be listening
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/loki/api/v1/labels", nil)

	NewServeMux(logger, cfg).ServeHTTP(rr, req)

	// Should return 502 Bad Gateway for connection errors
	require.Equal(t, http.StatusBadGateway, rr.Code)
	require.Equal(t, "text/plain; charset=utf-8", rr.Header().Get("Content-Type"))
	require.Equal(t, "sg1", rr.Header().Get("Failed-Backend"))
	// Response should include backend name and error message
	responseBody := rr.Body.String()
	require.Contains(t, responseBody, "sg1:")
	require.Contains(t, responseBody, "connection refused")
}

func TestProxy_NoHealthyUpstreams_Returns502(t *testing.T) {
	logger := log.NewNopLogger()

	// Use an unreachable URL that will fail to connect
	cfg := mkConfig("http://127.0.0.1:1") // Port 1 is unlikely to be listening
	rr := httptest.NewRecorder()
	// Use a path that falls through to forwardFirstResponse
	req := httptest.NewRequest(http.MethodGet, "/some/unknown/path", nil)

	NewServeMux(logger, cfg).ServeHTTP(rr, req)

	// Should return 502 Bad Gateway when no upstreams respond
	require.Equal(t, http.StatusBadGateway, rr.Code)
	require.Equal(t, "sg1", rr.Header().Get("Failed-Backend"))
	// With fail-fast error handling, connection errors are reported with backend context
	responseBody := rr.Body.String()
	require.Contains(t, responseBody, "sg1:")
	require.Contains(t, responseBody, "connection refused")
}

// roundTripFunc lets us use a plain function as an http.RoundTripper in tests.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestRoundTrip_GzipDecompression(t *testing.T) {
	plainText := []byte("hello from upstream")
	gzipped := mkGzip(plainText)

	inner := roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Encoding": []string{"gzip"}},
			Body:       io.NopCloser(bytes.NewReader(gzipped)),
		}, nil
	})

	rt := &CustomRoundTripper{rt: inner, logger: log.NewNopLogger()}
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	resp, err := rt.RoundTrip(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, plainText, body)
}

func TestCreateHTTPClient_NoTLS(t *testing.T) {
	sg := cfg.ServerGroup{Name: "loki1", URL: "http://localhost:3100"}
	client, err := createHTTPClient(sg, log.NewNopLogger())
	require.NoError(t, err)
	require.NotNil(t, client)
}

func TestCreateHTTPClient_InsecureSkipVerify(t *testing.T) {
	sg := cfg.ServerGroup{Name: "loki1", URL: "https://localhost:3100"}
	sg.HTTPClientConfig.TLSConfig.InsecureSkipVerify = true

	client, err := createHTTPClient(sg, log.NewNopLogger())
	require.NoError(t, err)
	require.NotNil(t, client)
}

func TestCreateHTTPClient_InvalidCAFile(t *testing.T) {
	sg := cfg.ServerGroup{Name: "loki1", URL: "https://localhost:3100"}
	sg.HTTPClientConfig.TLSConfig.CAFile = "/nonexistent/ca.pem"

	_, err := createHTTPClient(sg, log.NewNopLogger())
	require.Error(t, err)
}

func TestCreateHTTPClient_InvalidCertKeyPair(t *testing.T) {
	sg := cfg.ServerGroup{Name: "loki1", URL: "https://localhost:3100"}
	sg.HTTPClientConfig.TLSConfig.CertFile = "/nonexistent/cert.pem"
	sg.HTTPClientConfig.TLSConfig.KeyFile = "/nonexistent/key.pem"

	_, err := createHTTPClient(sg, log.NewNopLogger())
	require.Error(t, err)
}

func TestForwardFirstResponse_MultipleBackends(t *testing.T) {
	logger := log.NewNopLogger()

	body1 := "first backend body"
	body2 := "second backend body"

	results := make(chan *proxyresponse.BackendResponse, 2)
	results <- &proxyresponse.BackendResponse{
		BackendName: "loki1",
		Response: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"X-From": []string{"loki1"}},
			Body:       io.NopCloser(strings.NewReader(body1)),
		},
	}
	results <- &proxyresponse.BackendResponse{
		BackendName: "loki2",
		Response: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"X-From": []string{"loki2"}},
			Body:       io.NopCloser(strings.NewReader(body2)),
		},
	}
	close(results)

	w := httptest.NewRecorder()
	forwardFirstResponse(t.Context(), w, results, logger)

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, body1, w.Body.String())
}

func TestForwardFirstResponse_NoResponses(t *testing.T) {
	logger := log.NewNopLogger()

	results := make(chan *proxyresponse.BackendResponse)
	close(results)

	w := httptest.NewRecorder()
	forwardFirstResponse(t.Context(), w, results, logger)

	require.Equal(t, http.StatusBadGateway, w.Code)
	require.Contains(t, w.Body.String(), "No healthy upstreams available")
}

func TestFanoutRequest_RequestBodyReadError(t *testing.T) {
	logger := log.NewNopLogger()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	config := mkConfig(upstream.URL)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/loki/api/v1/labels", &errorReader{})

	NewServeMux(logger, config).ServeHTTP(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code)
}

// errorReader always returns an error when Read is called.
type errorReader struct{}

func (e *errorReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestProxyHandler_HTTPClientCreationFailure(t *testing.T) {
	// If a ServerGroup has an invalid CA file, createHTTPClient fails during
	// proxyHandler construction; the backend entry is skipped (no client added).
	// A request to that path hits the "missing HTTP client" BackendError → 502.
	logger := log.NewNopLogger()

	badCfg := &cfg.Config{
		ServerGroups: []cfg.ServerGroup{
			{
				Name: "badCA",
				URL:  "http://localhost:3100",
				HTTPClientConfig: func() cfg.HTTPClientConfig {
					hc := cfg.HTTPClientConfig{}
					hc.TLSConfig.CAFile = "/nonexistent/ca.pem"
					return hc
				}(),
			},
		},
	}

	mux := NewServeMux(logger, badCfg)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/loki/api/v1/labels", nil)
	mux.ServeHTTP(rr, req)

	// No client was created, so fanout returns "missing HTTP client" → 502.
	require.Equal(t, http.StatusBadGateway, rr.Code)
}

func TestRoundTrip_UpstreamError(t *testing.T) {
	// Covers the error-return path when the inner RoundTripper fails.
	inner := roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return nil, io.ErrUnexpectedEOF
	})

	rt := &CustomRoundTripper{rt: inner, logger: log.NewNopLogger()}
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	resp, err := rt.RoundTrip(req)
	require.Error(t, err)
	require.Nil(t, resp)
}

func TestRoundTrip_GzipNewReaderError(t *testing.T) {
	// Covers the branch where response claims gzip but body is not valid gzip.
	inner := roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Encoding": []string{"gzip"}},
			Body:       io.NopCloser(strings.NewReader("not gzip data")),
		}, nil
	})

	rt := &CustomRoundTripper{rt: inner, logger: log.NewNopLogger()}
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	resp, err := rt.RoundTrip(req)
	require.Error(t, err)
	require.Nil(t, resp)
}

func TestRoundTrip_NoMarshalWhenDebugDisabled(t *testing.T) {
	// Create a logger that only allows error level (debug is filtered out)
	var buf bytes.Buffer
	logger := log.NewLogfmtLogger(&buf)
	logger = level.NewFilter(logger, level.AllowError())

	body := []byte(`{"status":"ok"}`)
	inner := roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{},
			Body:       io.NopCloser(bytes.NewReader(body)),
		}, nil
	})

	rt := &CustomRoundTripper{rt: inner, logger: logger}
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer secret")

	resp, err := rt.RoundTrip(req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	_ = resp.Body.Close()

	// Debug output should NOT contain headers (marshal was skipped)
	require.NotContains(t, buf.String(), "Custom RoundTrip")
	require.NotContains(t, buf.String(), "Bearer secret")
}

func TestRoundTrip_MarshalWhenDebugEnabled(t *testing.T) {
	var buf bytes.Buffer
	logger := log.NewLogfmtLogger(&buf)
	logger = level.NewFilter(logger, level.AllowDebug())

	body := []byte(`{"status":"ok"}`)
	inner := roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{},
			Body:       io.NopCloser(bytes.NewReader(body)),
		}, nil
	})

	rt := &CustomRoundTripper{rt: inner, logger: logger}
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer secret")

	resp, err := rt.RoundTrip(req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	_ = resp.Body.Close()

	// Debug log line should appear
	require.Contains(t, buf.String(), "Custom RoundTrip")
	// But Authorization value should be redacted, not leaked
	require.NotContains(t, buf.String(), "Bearer secret")
	require.Contains(t, buf.String(), "REDACTED")
}

func TestFanout_NoHeaderLoggingWhenDebugOff(t *testing.T) {
	var buf bytes.Buffer
	logger := log.NewLogfmtLogger(&buf)
	logger = level.NewFilter(logger, level.AllowInfo())

	srv := mkUpstreamServer(t, map[string]http.HandlerFunc{
		"/loki/api/v1/labels": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"status":"success","data":["app"]}`)
		},
	})
	defer srv.Close()

	config := mkConfig(srv.URL)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/loki/api/v1/labels", nil)
	req.Header.Set("X-Custom", "should-not-appear")

	NewServeMux(logger, config).ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.NotContains(t, buf.String(), "Request Header")
	require.NotContains(t, buf.String(), "should-not-appear")
}

func TestFanout_HeaderLoggingRedactsSensitiveValues(t *testing.T) {
	var buf bytes.Buffer
	logger := log.NewLogfmtLogger(&buf)
	logger = level.NewFilter(logger, level.AllowDebug())

	srv := mkUpstreamServer(t, map[string]http.HandlerFunc{
		"/loki/api/v1/labels": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"status":"success","data":["app"]}`)
		},
	})
	defer srv.Close()

	config := mkConfig(srv.URL)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/loki/api/v1/labels", nil)
	req.Header.Set("Authorization", "Bearer top-secret-token")
	req.Header.Set("X-Safe-Header", "visible-value")

	NewServeMux(logger, config).ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	// Debug header logging should appear
	require.Contains(t, buf.String(), "Request Header")
	// Non-sensitive header value should be visible
	require.Contains(t, buf.String(), "visible-value")
	// Sensitive Authorization value should be redacted
	require.NotContains(t, buf.String(), "top-secret-token")
	require.Contains(t, buf.String(), "REDACTED")
}
