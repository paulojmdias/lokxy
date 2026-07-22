package acl

import (
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseSelectors(t *testing.T) {
	tests := []struct {
		name      string
		query     string
		wantErr   bool
		wantCount int // number of selector groups
	}{
		{name: "simple selector", query: `{app="x", ns="y"}`, wantCount: 1},
		{name: "empty selector", query: `{}`, wantCount: 1},
		{name: "catch-all regex", query: `{app=~".*"}`, wantCount: 1},
		{name: "log pipeline", query: `{app="x"} |= "err"`, wantCount: 1},
		{name: "metric query", query: `rate({app="x"}[5m])`, wantCount: 1},
		{name: "binary op two groups", query: `rate({app="a"}[5m]) / rate({app="b"}[5m])`, wantCount: 2},
		{name: "selector-less sample expr", query: `vector(1)`, wantCount: 1},
		{name: "unparseable", query: `{app=`, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sels, err := parseSelectors(tt.query)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Len(t, sels, tt.wantCount)
		})
	}
}

func TestSelector_IsEmpty(t *testing.T) {
	tests := []struct {
		name  string
		query string
		empty bool
	}{
		{name: "no matchers", query: `{}`, empty: true},
		{name: "catch-all star", query: `{app=~".*"}`, empty: true},
		{name: "catch-all plus", query: `{app=~".+"}`, empty: true},
		{name: "not-equal-empty", query: `{app!=""}`, empty: true},
		{name: "all catch-all", query: `{app=~".*", ns=~".+"}`, empty: true},
		{name: "real matcher", query: `{app="x"}`, empty: false},
		{name: "mixed real and catch-all", query: `{app=~".*", ns="prod"}`, empty: false},
		{name: "exotic catch-all not detected", query: `{app=~"[\\s\\S]*"}`, empty: false},
		{name: "selector-less sample expr is empty", query: `vector(1)`, empty: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sels, err := parseSelectors(tt.query)
			require.NoError(t, err)
			require.Len(t, sels, 1)
			require.Equal(t, tt.empty, sels[0].isEmpty())
		})
	}
}

func TestExtractQueries_GET(t *testing.T) {
	r := httptest.NewRequest("GET", "/loki/api/v1/query_range?query="+`{app="x"}`, nil)
	queries, ok := extractQueries(r, endpoint{param: "query"}, nopLogger())
	require.True(t, ok)
	require.Equal(t, []string{`{app="x"}`}, queries)
}

func TestExtractQueries_POST_RestoresBody(t *testing.T) {
	body := "query=" + `{app="x"}`
	r := httptest.NewRequest("POST", "/loki/api/v1/query_range", stringReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	queries, ok := extractQueries(r, endpoint{param: "query"}, nopLogger())
	require.True(t, ok)
	require.Equal(t, []string{`{app="x"}`}, queries)

	// The downstream fan-out must still be able to read the body.
	got := readAll(t, r.Body)
	require.Equal(t, body, got)
}

func TestExtractQueries_SeriesMultiMatch(t *testing.T) {
	r := httptest.NewRequest("GET", `/loki/api/v1/series?match[]={app="a"}&match[]={app="b"}`, nil)
	queries, ok := extractQueries(r, endpoint{param: "match[]", multi: true}, nopLogger())
	require.True(t, ok)
	require.ElementsMatch(t, []string{`{app="a"}`, `{app="b"}`}, queries)
}
