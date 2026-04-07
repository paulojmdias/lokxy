package victorialogs

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/VictoriaMetrics-Community/logql-to-logsql/lib/logsql"
	"github.com/stretchr/testify/require"
)

func TestTranslateQuery_LogQuery(t *testing.T) {
	qi, err := TranslateQuery(`{app="nginx"}`)
	require.NoError(t, err)
	require.NotNil(t, qi)
	require.Equal(t, logsql.QueryKindLogs, qi.Kind)
	require.NotEmpty(t, qi.LogsQL)
}

func TestTranslateQuery_StatsQuery(t *testing.T) {
	qi, err := TranslateQuery(`rate({app="nginx"} [5m])`)
	require.NoError(t, err)
	require.NotNil(t, qi)
	require.Equal(t, logsql.QueryKindStats, qi.Kind)
	require.NotEmpty(t, qi.LogsQL)
}

func TestTranslateQuery_WithFilter(t *testing.T) {
	qi, err := TranslateQuery(`{app="nginx"} |= "error"`)
	require.NoError(t, err)
	require.NotNil(t, qi)
	require.Equal(t, logsql.QueryKindLogs, qi.Kind)
	require.NotEmpty(t, qi.LogsQL)
}

func TestTranslateQuery_Empty(t *testing.T) {
	_, err := TranslateQuery("")
	require.Error(t, err)
}

func TestTranslateQuery_Invalid(t *testing.T) {
	_, err := TranslateQuery("not a valid logql query %%$#@")
	require.Error(t, err)
}

func TestDefaultQueryInfo(t *testing.T) {
	qi := DefaultQueryInfo()
	require.NotNil(t, qi)
	require.Equal(t, logsql.QueryKindLogs, qi.Kind)
	require.Equal(t, "*", qi.LogsQL)
}

func TestExtractLogQLQuery_GET(t *testing.T) {
	r := &http.Request{
		URL: &url.URL{
			Path:     "/loki/api/v1/query_range",
			RawQuery: `query={app="nginx"}`,
		},
	}
	q := ExtractLogQLQuery(r, nil)
	require.Equal(t, `{app="nginx"}`, q)
}

func TestExtractLogQLQuery_POST(t *testing.T) {
	body := []byte(`query={app="web"}&start=0&end=1`)
	r := &http.Request{
		URL: &url.URL{
			Path: "/loki/api/v1/query_range",
		},
	}
	q := ExtractLogQLQuery(r, body)
	require.Equal(t, `{app="web"}`, q)
}

func TestExtractLogQLQuery_GETPrecedence(t *testing.T) {
	body := []byte(`query={app="post"}`)
	r := &http.Request{
		URL: &url.URL{
			Path:     "/loki/api/v1/query_range",
			RawQuery: `query={app="get"}`,
		},
	}
	q := ExtractLogQLQuery(r, body)
	// GET takes precedence.
	require.Equal(t, `{app="get"}`, q)
}

func TestExtractLogQLQuery_Empty(t *testing.T) {
	r := &http.Request{
		URL: &url.URL{
			Path: "/loki/api/v1/labels",
		},
	}
	q := ExtractLogQLQuery(r, nil)
	require.Equal(t, "", q)
}

func TestExtractLabelName(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{
			name: "hostname",
			path: "/loki/api/v1/label/hostname/values",
			want: "hostname",
		},
		{
			name: "app",
			path: "/loki/api/v1/label/app/values",
			want: "app",
		},
		{
			name: "no values suffix",
			path: "/loki/api/v1/label/hostname",
			want: "",
		},
		{
			name: "wrong prefix",
			path: "/api/v1/label/hostname/values",
			want: "",
		},
		{
			name: "empty name",
			path: "/loki/api/v1/label//values",
			want: "",
		},
		{
			name: "name with slash",
			path: "/loki/api/v1/label/a/b/values",
			want: "",
		},
		{
			name: "labels path (no name)",
			path: "/loki/api/v1/labels",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractLabelName(tt.path)
			require.Equal(t, tt.want, got)
		})
	}
}

// --- sanitizeLogsQL (Bug 2 & 3 fixes) ---

func TestStripEmptyStringFilter(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "trailing empty string",
			input: `app:"nginx" ""`,
			want:  `app:"nginx"`,
		},
		{
			name:  "middle empty string",
			input: `app:"nginx" "" error`,
			want:  `app:"nginx" error`,
		},
		{
			name:  "no empty string",
			input: `app:"nginx" "error"`,
			want:  `app:"nginx" "error"`,
		},
		{
			name:  "only empty string",
			input: `""`,
			want:  ``,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripEmptyStringFilter(tt.input)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestStripTimeFilter(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "with _time:1m",
			input: `app:"nginx" _time:1m | stats count() as value`,
			want:  `app:"nginx" | stats count() as value`,
		},
		{
			name:  "with _time:5m",
			input: `app:"nginx" _time:5m`,
			want:  `app:"nginx"`,
		},
		{
			name:  "no _time filter",
			input: `app:"nginx" | stats count() as value`,
			want:  `app:"nginx" | stats count() as value`,
		},
		{
			name:  "with _time:1h30m",
			input: `_time:1h30m app:"nginx"`,
			want:  ` app:"nginx"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripTimeFilter(tt.input)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestSanitizeLogsQL(t *testing.T) {
	// For stats queries, both "" and _time filters should be stripped.
	input := `app:"nginx" _time:1m "" | stats count() as value`
	got := sanitizeLogsQL(input, logsql.QueryKindStats)
	require.Equal(t, `app:"nginx" | stats count() as value`, got)

	// For log queries, only "" is stripped; _time is kept.
	input = `app:"nginx" _time:1m ""`
	got = sanitizeLogsQL(input, logsql.QueryKindLogs)
	require.Equal(t, `app:"nginx" _time:1m`, got)
}

func TestTranslateQuery_EmptyStringFilterStripped(t *testing.T) {
	// {app="nginx"} |= "" should translate and have "" stripped.
	qi, err := TranslateQuery(`{app="nginx"} |= ""`)
	require.NoError(t, err)
	require.NotContains(t, qi.LogsQL, `""`)
}
