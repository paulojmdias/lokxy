package routing

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/stretchr/testify/require"

	cfg "github.com/paulojmdias/lokxy/pkg/config"
)

func TestRoutingKeys(t *testing.T) {
	tests := []struct {
		name   string
		groups []cfg.ServerGroup
		want   []string
	}{
		{
			name:   "no labels",
			groups: []cfg.ServerGroup{{Name: "a"}, {Name: "b"}},
			want:   nil,
		},
		{
			name: "overlapping keys deduped",
			groups: []cfg.ServerGroup{
				{Name: "a", Labels: map[string]string{"__sg__": "loki1", "env": "prod"}},
				{Name: "b", Labels: map[string]string{"__sg__": "loki2"}},
			},
			want: []string{"__sg__", "env"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keys := RoutingKeys(tt.groups)
			require.Len(t, keys, len(tt.want))
			for _, k := range tt.want {
				_, ok := keys[k]
				require.True(t, ok, "expected key %q", k)
			}
		})
	}
}

func newReq(t *testing.T, method, target string, body string, contentType string) *http.Request {
	t.Helper()
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
	} else {
		r = httptest.NewRequest(method, target, nil)
	}
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	return r
}

func TestExtractMatchers(t *testing.T) {
	tests := []struct {
		name        string
		method      string
		target      string
		body        string
		contentType string
		wantOK      bool
		wantNames   []string
	}{
		{
			name:      "GET log selector",
			method:    http.MethodGet,
			target:    `/loki/api/v1/query_range?query={__sg__="loki1",app="foo"}`,
			wantOK:    true,
			wantNames: []string{"__sg__", "app"},
		},
		{
			name:      "GET metric query",
			method:    http.MethodGet,
			target:    `/loki/api/v1/query_range?query=sum(rate({__sg__=~"loki.*"}[5m]))`,
			wantOK:    true,
			wantNames: []string{"__sg__"},
		},
		{
			name:        "POST form body query",
			method:      http.MethodPost,
			target:      "/loki/api/v1/query_range",
			body:        `query=` + url.QueryEscape(`{__sg__="a"}`),
			contentType: "application/x-www-form-urlencoded",
			wantOK:      true,
			wantNames:   []string{"__sg__"},
		},
		{
			name:        "POST form body with charset",
			method:      http.MethodPost,
			target:      "/loki/api/v1/query_range",
			body:        `query=` + url.QueryEscape(`{__sg__="a"}`),
			contentType: "application/x-www-form-urlencoded; charset=UTF-8",
			wantOK:      true,
			wantNames:   []string{"__sg__"},
		},
		{
			name:      "series multiple match[]",
			method:    http.MethodGet,
			target:    `/loki/api/v1/series?match[]=` + url.QueryEscape(`{__sg__="a"}`) + `&match[]=` + url.QueryEscape(`{job="b"}`),
			wantOK:    true,
			wantNames: []string{"__sg__", "job"},
		},
		{
			name:   "no selector (labels endpoint)",
			method: http.MethodGet,
			target: "/loki/api/v1/labels",
			wantOK: false,
		},
		{
			name:   "malformed query falls back",
			method: http.MethodGet,
			target: `/loki/api/v1/query_range?query=` + url.QueryEscape("this is not logql"),
			wantOK: false,
		},
		{
			name:   "malformed match[] falls back",
			method: http.MethodGet,
			target: `/loki/api/v1/series?match[]=not-a-selector`,
			wantOK: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newReq(t, tt.method, tt.target, tt.body, tt.contentType)
			// Simulate the buffered body the proxy holds.
			var bodyBytes []byte
			if tt.body != "" {
				bodyBytes = []byte(tt.body)
			}
			matchers, ok := ExtractMatchers(r, bodyBytes)
			require.Equal(t, tt.wantOK, ok)
			if !tt.wantOK {
				require.Nil(t, matchers)
				return
			}
			gotNames := make([]string, 0, len(matchers))
			for _, m := range matchers {
				gotNames = append(gotNames, m.Name)
			}
			require.ElementsMatch(t, tt.wantNames, gotNames)
		})
	}
}

func TestExtractMatchers_BodyUntouched(t *testing.T) {
	body := `query=` + url.QueryEscape(`{__sg__="a"}`)
	r := newReq(t, http.MethodPost, "/loki/api/v1/query_range", body, "application/x-www-form-urlencoded")
	bodyBytes := []byte(body)
	_, ok := ExtractMatchers(r, bodyBytes)
	require.True(t, ok)
	// bodyBytes must be untouched so the proxy can replay it to every group.
	require.Equal(t, body, string(bodyBytes))
}

func TestExtractMatchers_NonFormBodyIgnored(t *testing.T) {
	// A JSON content-type body must not be parsed as form data.
	body := `query=` + url.QueryEscape(`{__sg__="a"}`)
	r := newReq(t, http.MethodPost, "/loki/api/v1/query_range", body, "application/json")
	_, ok := ExtractMatchers(r, []byte(body))
	require.False(t, ok)
}

func mustMatcher(t *testing.T, typ labels.MatchType, name, val string) *labels.Matcher {
	t.Helper()
	m, err := labels.NewMatcher(typ, name, val)
	require.NoError(t, err)
	return m
}

func TestSelectGroups(t *testing.T) {
	groups := []cfg.ServerGroup{
		{Name: "loki1", Labels: map[string]string{"__sg__": "loki1"}},
		{Name: "loki2", Labels: map[string]string{"__sg__": "loki2"}},
		{Name: "nolabel"}, // nil Labels
	}
	routingKeys := RoutingKeys(groups)

	tests := []struct {
		name     string
		matchers []*labels.Matcher
		want     []string
	}{
		{
			name:     "equal selects one",
			matchers: []*labels.Matcher{mustMatcher(t, labels.MatchEqual, "__sg__", "loki1")},
			want:     []string{"loki1"},
		},
		{
			name:     "not-equal excludes match, includes absent",
			matchers: []*labels.Matcher{mustMatcher(t, labels.MatchNotEqual, "__sg__", "loki1")},
			want:     []string{"loki2", "nolabel"},
		},
		{
			name:     "regex",
			matchers: []*labels.Matcher{mustMatcher(t, labels.MatchRegexp, "__sg__", "loki.*")},
			want:     []string{"loki1", "loki2"},
		},
		{
			name:     "not-regex includes absent",
			matchers: []*labels.Matcher{mustMatcher(t, labels.MatchNotRegexp, "__sg__", "loki1")},
			want:     []string{"loki2", "nolabel"},
		},
		{
			name:     "non-routing matcher ignored -> all groups",
			matchers: []*labels.Matcher{mustMatcher(t, labels.MatchEqual, "app", "foo")},
			want:     []string{"loki1", "loki2", "nolabel"},
		},
		{
			name: "routing + non-routing: only routing filters",
			matchers: []*labels.Matcher{
				mustMatcher(t, labels.MatchEqual, "__sg__", "loki1"),
				mustMatcher(t, labels.MatchEqual, "app", "foo"),
			},
			want: []string{"loki1"},
		},
		{
			name:     "no match -> empty",
			matchers: []*labels.Matcher{mustMatcher(t, labels.MatchEqual, "__sg__", "nope")},
			want:     []string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SelectGroups(groups, tt.matchers, routingKeys)
			gotNames := make([]string, 0, len(got))
			for _, g := range got {
				gotNames = append(gotNames, g.Name)
			}
			require.ElementsMatch(t, tt.want, gotNames)
		})
	}
}

func TestStripQuery(t *testing.T) {
	routingKeys := map[string]struct{}{"__sg__": {}}

	tests := []struct {
		name        string
		query       string
		wantChanged bool
		wantEmpty   bool
		wantContain string // substring the result must contain (when changed)
		wantMissing string // substring the result must NOT contain (when changed)
	}{
		{
			name:        "strip from log selector with pipeline",
			query:       `{__sg__="a",app="foo"} |= "err"`,
			wantChanged: true,
			wantEmpty:   false,
			wantContain: `app="foo"`,
			wantMissing: "__sg__",
		},
		{
			name:        "strip from metric query",
			query:       `sum(rate({__sg__=~"loki.*",app="foo"}[5m]))`,
			wantChanged: true,
			wantEmpty:   false,
			wantContain: `app="foo"`,
			wantMissing: "__sg__",
		},
		{
			name:        "strip leaves empty selector",
			query:       `{__sg__="a"}`,
			wantChanged: true,
			wantEmpty:   true,
		},
		{
			name:        "nothing to strip",
			query:       `{app="foo"}`,
			wantChanged: false,
		},
		{
			name:        "unparseable query returned as-is",
			query:       `not logql`,
			wantChanged: false,
		},
		{
			name:        "empty query",
			query:       ``,
			wantChanged: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, changed, empty := StripQuery(tt.query, routingKeys)
			require.Equal(t, tt.wantChanged, changed)
			require.Equal(t, tt.wantEmpty, empty)
			if !changed {
				require.Equal(t, tt.query, got)
				return
			}
			if tt.wantContain != "" {
				require.Contains(t, got, tt.wantContain)
			}
			if tt.wantMissing != "" {
				require.NotContains(t, got, tt.wantMissing)
			}
		})
	}
}

func TestStripRequest(t *testing.T) {
	routingKeys := map[string]struct{}{"__sg__": {}}

	t.Run("GET url query stripped", func(t *testing.T) {
		r := newReq(t, http.MethodGet, `/loki/api/v1/query_range?query=`+url.QueryEscape(`{__sg__="a",app="foo"}`)+`&limit=100`, "", "")
		rawQuery, body, empty := StripRequest(r, nil, routingKeys)
		require.False(t, empty)
		require.Nil(t, body)
		// re-parse to assert semantic content independent of encoding/order
		vals, err := url.ParseQuery(rawQuery)
		require.NoError(t, err)
		require.Equal(t, `{app="foo"}`, vals.Get("query"))
		require.Equal(t, "100", vals.Get("limit"))
	})

	t.Run("POST form body stripped, url untouched", func(t *testing.T) {
		body := `query=` + url.QueryEscape(`{__sg__="a",app="foo"}`)
		r := newReq(t, http.MethodPost, "/loki/api/v1/query_range", body, "application/x-www-form-urlencoded")
		rawQuery, newBody, empty := StripRequest(r, []byte(body), routingKeys)
		require.False(t, empty)
		require.Equal(t, "", rawQuery)
		vals, err := url.ParseQuery(string(newBody))
		require.NoError(t, err)
		require.Equal(t, `{app="foo"}`, vals.Get("query"))
		// caller's buffer untouched
		require.Equal(t, `query=`+url.QueryEscape(`{__sg__="a",app="foo"}`), body)
	})

	t.Run("match[] stripped", func(t *testing.T) {
		r := newReq(t, http.MethodGet, `/loki/api/v1/series?match[]=`+url.QueryEscape(`{__sg__="a",job="b"}`), "", "")
		rawQuery, _, empty := StripRequest(r, nil, routingKeys)
		require.False(t, empty)
		vals, err := url.ParseQuery(rawQuery)
		require.NoError(t, err)
		require.Equal(t, `{job="b"}`, vals.Get("match[]"))
	})

	t.Run("empty selector reported", func(t *testing.T) {
		r := newReq(t, http.MethodGet, `/loki/api/v1/query_range?query=`+url.QueryEscape(`{__sg__="a"}`), "", "")
		_, _, empty := StripRequest(r, nil, routingKeys)
		require.True(t, empty)
	})

	t.Run("nothing stripped keeps original", func(t *testing.T) {
		orig := `/loki/api/v1/query_range?query=` + url.QueryEscape(`{app="foo"}`)
		r := newReq(t, http.MethodGet, orig, "", "")
		rawQuery, body, empty := StripRequest(r, nil, routingKeys)
		require.False(t, empty)
		require.Nil(t, body)
		require.Equal(t, r.URL.RawQuery, rawQuery)
	})
}

func TestStripQuery_NoRoutingKeys(t *testing.T) {
	got, changed, empty := StripQuery(`{__sg__="a"}`, map[string]struct{}{})
	require.False(t, changed)
	require.False(t, empty)
	require.Equal(t, `{__sg__="a"}`, got)
}
