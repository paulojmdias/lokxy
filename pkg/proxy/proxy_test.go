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
	"testing"

	"github.com/go-kit/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cfg "github.com/paulojmdias/lokxy/pkg/config"
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
		mux.HandleFunc(p, h)
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"default":true}`)
	})
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
		"/loki/api/v1/labels": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"labels":["a","b"]}`)
		},
	})
	defer s1.Close()

	s2 := mkUpstreamServer(t, map[string]http.HandlerFunc{
		"/loki/api/v1/labels": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"labels":["c"]}`)
		},
	})
	defer s2.Close()

	orig := apiRoutes
	defer func() { apiRoutes = orig }()

	apiRoutes = map[string]func(http.ResponseWriter, <-chan *http.Response, log.Logger){
		"/loki/api/v1/labels": func(w http.ResponseWriter, results <-chan *http.Response, logger log.Logger) {
			count := 0
			for resp := range results {
				count++
				_ = resp.Body.Close()
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"instances": count})
		},
	}

	config := mkConfig(s1.URL, s2.URL)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/loki/api/v1/labels", nil)
	ProxyHandler(rr, req, config, logger)

	require.Equal(t, http.StatusOK, rr.Code)

	var got map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	assert.Equal(t, float64(2), got["instances"])
}

func TestProxy_DetectedFieldValues_PathExtractionAndMerge(t *testing.T) {
	logger := log.NewNopLogger()
	encoded := url.PathEscape("foo/bar")
	upPath := "/loki/api/v1/detected_field/" + encoded + "/values"

	s1 := mkUpstreamServer(t, map[string]http.HandlerFunc{
		upPath: func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"field":"ignored","values":[{"value":"X","count":1},{"value":"Y","count":2}]}`)
		},
	})
	defer s1.Close()

	s2 := mkUpstreamServer(t, map[string]http.HandlerFunc{
		upPath: func(w http.ResponseWriter, r *http.Request) {
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

	ProxyHandler(rr, req, config, logger)
	require.Equal(t, http.StatusOK, rr.Code)

	var out struct {
		Field  string `json:"field"`
		Values []struct {
			Value string `json:"value"`
			Count int    `json:"count"`
		} `json:"values"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &out))

	assert.Equal(t, "foo/bar", out.Field)
	values := map[string]int{}
	for _, v := range out.Values {
		values[v.Value] = v.Count
	}
	assert.Equal(t, 4, values["X"])
	assert.Equal(t, 2, values["Y"])
}

func TestProxy_UnknownPath_ForwardsFirstResponseWithGzipBody(t *testing.T) {
	logger := log.NewNopLogger()

	plain := []byte(`{"hello":"world"}`)
	gz := mkGzip(plain)

	s1 := mkUpstreamServer(t, map[string]http.HandlerFunc{
		"/unknown": func(w http.ResponseWriter, r *http.Request) {
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

	ProxyHandler(rr, req, config, logger)
	require.Equal(t, http.StatusOK, rr.Code)
	assert.JSONEq(t, string(plain), rr.Body.String())
}

func Test_extractDetectedFieldName(t *testing.T) {
	okCases := map[string]string{
		"/loki/api/v1/detected_field/job/values":                  "job",
		"/loki/api/v1/detected_field/foo%2Fbar/values":            "foo/bar",
		"/loki/api/v1/detected_field/%5Bcomplex%5D%20name/values": "[complex] name",
	}
	for in, want := range okCases {
		got, ok := extractDetectedFieldName(in)
		require.True(t, ok)
		assert.Equal(t, want, got)
	}

	bad := []string{
		"/loki/api/v1/detected_field",
		"/loki/api/v1/detected_field/job",
		"/loki/api/v1/detected_field//values",
		"/loki/api/v1/detected_field/job/values/extra",
	}
	for _, in := range bad {
		_, ok := extractDetectedFieldName(in)
		assert.False(t, ok)
	}
}
