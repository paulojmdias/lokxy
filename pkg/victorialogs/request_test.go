package victorialogs

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/VictoriaMetrics-Community/logql-to-logsql/lib/logsql"
	"github.com/stretchr/testify/require"
)

func TestRewriteRequest_LogQuery(t *testing.T) {
	orig := &http.Request{
		Method: http.MethodGet,
		URL: &url.URL{
			Path:     LokiPathQueryRange,
			RawQuery: "query=" + url.QueryEscape(`{app="nginx"}`) + "&start=1704067200&end=1704070800&limit=100",
		},
		Header: http.Header{},
	}

	qi := &logsql.QueryInfo{
		Kind:   logsql.QueryKindLogs,
		LogsQL: `app:nginx`,
	}

	req, err := RewriteRequest(context.Background(), orig, qi, "https://vlogs.example.com", "", nil)
	require.NoError(t, err)
	require.NotNil(t, req)

	require.Equal(t, http.MethodPost, req.Method)
	require.Equal(t, "https://vlogs.example.com/select/logsql/query", req.URL.String())
	require.Equal(t, "application/x-www-form-urlencoded", req.Header.Get("Content-Type"))

	body, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	form, err := url.ParseQuery(string(body))
	require.NoError(t, err)
	require.Equal(t, "app:nginx", form.Get("query"))
	require.Equal(t, "1704067200", form.Get("start"))
	require.Equal(t, "1704070800", form.Get("end"))
	require.Equal(t, "100", form.Get("limit"))
}

func TestRewriteRequest_StatsQueryRange(t *testing.T) {
	orig := &http.Request{
		Method: http.MethodGet,
		URL: &url.URL{
			Path:     LokiPathQueryRange,
			RawQuery: "query=" + url.QueryEscape(`rate({app="nginx"} [5m])`) + "&start=100&end=200&step=60",
		},
		Header: http.Header{},
	}

	qi := &logsql.QueryInfo{
		Kind:   logsql.QueryKindStats,
		LogsQL: `app:nginx | stats count() as logs`,
	}

	req, err := RewriteRequest(context.Background(), orig, qi, "https://vlogs.example.com", "", nil)
	require.NoError(t, err)

	require.Equal(t, "https://vlogs.example.com/select/logsql/stats_query_range", req.URL.String())

	body, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	form, err := url.ParseQuery(string(body))
	require.NoError(t, err)
	require.Equal(t, "60", form.Get("step"))
}

func TestRewriteRequest_StatsQueryInstant(t *testing.T) {
	orig := &http.Request{
		Method: http.MethodGet,
		URL: &url.URL{
			Path:     LokiPathQuery,
			RawQuery: "query=" + url.QueryEscape(`rate({app="nginx"} [5m])`) + "&time=1704067200",
		},
		Header: http.Header{},
	}

	qi := &logsql.QueryInfo{
		Kind:   logsql.QueryKindStats,
		LogsQL: `app:nginx | stats count() as logs`,
	}

	req, err := RewriteRequest(context.Background(), orig, qi, "https://vlogs.example.com", "", nil)
	require.NoError(t, err)

	require.Equal(t, "https://vlogs.example.com/select/logsql/stats_query", req.URL.String())

	body, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	form, err := url.ParseQuery(string(body))
	require.NoError(t, err)
	require.Equal(t, "1704067200", form.Get("time"))
}

func TestRewriteRequest_WithTenant(t *testing.T) {
	orig := &http.Request{
		Method: http.MethodGet,
		URL: &url.URL{
			Path:     LokiPathQueryRange,
			RawQuery: "query=" + url.QueryEscape(`{app="nginx"}`),
		},
		Header: http.Header{},
	}

	qi := &logsql.QueryInfo{
		Kind:   logsql.QueryKindLogs,
		LogsQL: `app:nginx`,
	}

	req, err := RewriteRequest(context.Background(), orig, qi, "https://vlogs.example.com", "0:0", nil)
	require.NoError(t, err)
	require.Equal(t, "https://vlogs.example.com/0:0/select/logsql/query", req.URL.String())
}

func TestRewriteRequest_Labels(t *testing.T) {
	orig := &http.Request{
		Method: http.MethodGet,
		URL: &url.URL{
			Path:     LokiPathLabels,
			RawQuery: "start=100&end=200",
		},
		Header: http.Header{},
	}

	qi := DefaultQueryInfo()

	req, err := RewriteRequest(context.Background(), orig, qi, "https://vlogs.example.com", "", nil)
	require.NoError(t, err)
	require.Equal(t, "https://vlogs.example.com/select/logsql/field_names", req.URL.String())

	body, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	form, err := url.ParseQuery(string(body))
	require.NoError(t, err)
	require.Equal(t, "100", form.Get("start"))
	require.Equal(t, "200", form.Get("end"))
}

func TestRewriteRequest_LabelValues(t *testing.T) {
	orig := &http.Request{
		Method: http.MethodGet,
		URL: &url.URL{
			Path:     "/loki/api/v1/label/hostname/values",
			RawQuery: "start=100&end=200",
		},
		Header: http.Header{},
	}

	qi := DefaultQueryInfo()

	req, err := RewriteRequest(context.Background(), orig, qi, "https://vlogs.example.com", "", nil)
	require.NoError(t, err)
	require.Equal(t, "https://vlogs.example.com/select/logsql/field_values", req.URL.String())

	body, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	form, err := url.ParseQuery(string(body))
	require.NoError(t, err)
	require.Equal(t, "hostname", form.Get("field"))
}

func TestRewriteRequest_Series(t *testing.T) {
	orig := &http.Request{
		Method: http.MethodGet,
		URL: &url.URL{
			Path:     LokiPathSeries,
			RawQuery: "start=100&end=200",
		},
		Header: http.Header{},
	}

	qi := &logsql.QueryInfo{
		Kind:   logsql.QueryKindLogs,
		LogsQL: `app:nginx`,
	}

	req, err := RewriteRequest(context.Background(), orig, qi, "https://vlogs.example.com", "", nil)
	require.NoError(t, err)
	require.Equal(t, "https://vlogs.example.com/select/logsql/streams", req.URL.String())

	body, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	form, err := url.ParseQuery(string(body))
	require.NoError(t, err)
	require.Equal(t, "100", form.Get("start"))
	require.Equal(t, "200", form.Get("end"))
}

func TestRewriteRequest_UnsupportedEndpoint(t *testing.T) {
	orig := &http.Request{
		Method: http.MethodGet,
		URL: &url.URL{
			Path: LokiPathTail,
		},
		Header: http.Header{},
	}

	qi := DefaultQueryInfo()

	_, err := RewriteRequest(context.Background(), orig, qi, "https://vlogs.example.com", "", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no VictoriaLogs endpoint mapping")
}

func TestRewriteRequest_InvalidBaseURL(t *testing.T) {
	orig := &http.Request{
		Method: http.MethodGet,
		URL: &url.URL{
			Path:     LokiPathQueryRange,
			RawQuery: "query=" + url.QueryEscape(`{app="nginx"}`),
		},
		Header: http.Header{},
	}

	qi := DefaultQueryInfo()

	_, err := RewriteRequest(context.Background(), orig, qi, "://invalid", "", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid VictoriaLogs base URL")
}

func TestRewriteRequest_POSTBody(t *testing.T) {
	postBody := "query=" + url.QueryEscape(`{app="nginx"}`) + "&start=100&end=200&limit=50"
	orig := &http.Request{
		Method: http.MethodPost,
		URL: &url.URL{
			Path: LokiPathQueryRange,
		},
		Header: http.Header{"Content-Type": []string{"application/x-www-form-urlencoded"}},
	}

	qi := &logsql.QueryInfo{
		Kind:   logsql.QueryKindLogs,
		LogsQL: `app:nginx`,
	}

	req, err := RewriteRequest(context.Background(), orig, qi, "https://vlogs.example.com", "", []byte(postBody))
	require.NoError(t, err)

	body, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	form, err := url.ParseQuery(string(body))
	require.NoError(t, err)
	require.Equal(t, "app:nginx", form.Get("query"))
	require.Equal(t, "100", form.Get("start"))
	require.Equal(t, "200", form.Get("end"))
	require.Equal(t, "50", form.Get("limit"))
}

func TestCollectOrigParams_GETOnly(t *testing.T) {
	r := &http.Request{
		URL: &url.URL{
			RawQuery: "query=foo&start=100",
		},
	}

	params := CollectOrigParams(r, nil)
	require.Equal(t, "foo", params.Get("query"))
	require.Equal(t, "100", params.Get("start"))
}

func TestCollectOrigParams_POSTOnly(t *testing.T) {
	r := &http.Request{
		URL: &url.URL{},
	}

	params := CollectOrigParams(r, []byte("query=bar&end=200"))
	require.Equal(t, "bar", params.Get("query"))
	require.Equal(t, "200", params.Get("end"))
}

func TestCollectOrigParams_GETOverridesPOST(t *testing.T) {
	r := &http.Request{
		URL: &url.URL{
			RawQuery: "query=get_value",
		},
	}

	params := CollectOrigParams(r, []byte("query=post_value&extra=yes"))
	require.Equal(t, "get_value", params.Get("query"))
	require.Equal(t, "yes", params.Get("extra"))
}

func TestRewriteRequest_BaseURLWithTrailingSlash(t *testing.T) {
	orig := &http.Request{
		Method: http.MethodGet,
		URL: &url.URL{
			Path:     LokiPathLabels,
			RawQuery: "start=100",
		},
		Header: http.Header{},
	}
	qi := DefaultQueryInfo()

	req, err := RewriteRequest(context.Background(), orig, qi, "https://vlogs.example.com/", "", nil)
	require.NoError(t, err)
	// Should not double-slash.
	require.False(t, strings.Contains(req.URL.Path, "//"))
	require.Equal(t, "/select/logsql/field_names", req.URL.Path)
}

func TestRewriteRequest_IndexStats(t *testing.T) {
	orig := &http.Request{
		Method: http.MethodGet,
		URL: &url.URL{
			Path:     LokiPathIndexStats,
			RawQuery: "query=" + url.QueryEscape(`{app="nginx"}`) + "&start=100&end=200",
		},
		Header: http.Header{},
	}

	qi := &logsql.QueryInfo{
		Kind:   logsql.QueryKindStats,
		LogsQL: `app:nginx | stats count() as logs`,
	}

	req, err := RewriteRequest(context.Background(), orig, qi, "https://vlogs.example.com", "", nil)
	require.NoError(t, err)
	require.Equal(t, "https://vlogs.example.com/select/logsql/stats_query", req.URL.String())
}
