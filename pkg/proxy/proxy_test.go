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
	"testing"

	"github.com/go-kit/log"
	cfg "github.com/paulojmdias/lokxy/pkg/config"
	"github.com/stretchr/testify/require"
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
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		},
	})
	defer s1.Close()
	s2 := mkUpstreamServer(t, map[string]http.HandlerFunc{
		up: func(w http.ResponseWriter, r *http.Request) {
			defer r.Body.Close()
			b, _ := io.ReadAll(r.Body)
			got2 = string(b)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"ok"}`))
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
