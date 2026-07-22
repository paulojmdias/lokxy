// Package routing implements label-based server-group routing for lokxy.
//
// Server groups may define arbitrary labels (see config.ServerGroup.Labels).
// The union of every label key across all groups forms the set of
// "routing-label keys". When an incoming LogQL query's selector matches on one
// of those keys, the fan-out is restricted to only the groups whose label value
// satisfies the matcher, instead of querying every group. Because such routing
// labels (e.g. __sg__) are virtual and do not exist on the upstream Loki
// streams, the routing-key matchers are stripped from the query before it is
// forwarded to the selected groups.
package routing

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/grafana/loki/v3/pkg/logql/syntax"
	"github.com/prometheus/prometheus/model/labels"

	cfg "github.com/paulojmdias/lokxy/pkg/config"
)

// RoutingKeys returns the union of all label keys defined across the given
// server groups. An empty result means routing is disabled for the request
// (no group defines any label).
func RoutingKeys(groups []cfg.ServerGroup) map[string]struct{} {
	keys := make(map[string]struct{})
	for _, g := range groups {
		for k := range g.Labels {
			keys[k] = struct{}{}
		}
	}
	return keys
}

// ExtractMatchers pulls the LogQL label matchers from a request without
// disturbing the caller's buffered request body. It looks at, in order, the
// `query` parameter (from the URL and, for form-urlencoded POST bodies, from
// bodyBytes) and every `match[]` parameter (used by the series endpoint).
//
// It returns (matchers, true) when at least one selector was found and every
// selector parsed successfully. It returns (nil, false) when there is no
// selector at all or any selector fails to parse — in that case the caller must
// keep ALL groups, so routing can never break existing behavior.
func ExtractMatchers(r *http.Request, bodyBytes []byte) ([]*labels.Matcher, bool) {
	values := r.URL.Query()

	// Merge in form-urlencoded POST parameters (e.g. Grafana query_range) from
	// a copy of the buffered body. url.ParseQuery is side-effect-free, unlike
	// r.ParseForm which would consume the body the fan-out replays.
	if len(bodyBytes) > 0 && isFormURLEncoded(r) {
		if bodyValues, err := url.ParseQuery(string(bodyBytes)); err == nil {
			for k, vs := range bodyValues {
				values[k] = append(values[k], vs...)
			}
		}
	}

	var matchers []*labels.Matcher
	found := false

	if q := values.Get("query"); q != "" {
		found = true
		m, err := matchersFromQuery(q)
		if err != nil {
			return nil, false
		}
		matchers = append(matchers, m...)
	}

	for _, ms := range values["match[]"] {
		if ms == "" {
			continue
		}
		found = true
		m, err := syntax.ParseMatchers(ms, false)
		if err != nil {
			return nil, false
		}
		matchers = append(matchers, m...)
	}

	if !found {
		return nil, false
	}
	return matchers, true
}

// SelectGroups returns the subset of groups a request should fan out to. A
// group is kept iff, for every matcher whose name is a routing key,
// matcher.Matches(group.Labels[name]) is true (an absent label reads as "",
// matching Loki semantics). If no matcher references a routing key, all groups
// are returned unchanged.
func SelectGroups(groups []cfg.ServerGroup, matchers []*labels.Matcher, routingKeys map[string]struct{}) []cfg.ServerGroup {
	var routingMatchers []*labels.Matcher
	for _, m := range matchers {
		if _, ok := routingKeys[m.Name]; ok {
			routingMatchers = append(routingMatchers, m)
		}
	}
	if len(routingMatchers) == 0 {
		return groups
	}

	selected := make([]cfg.ServerGroup, 0, len(groups))
	for _, g := range groups {
		keep := true
		for _, m := range routingMatchers {
			if !m.Matches(g.Labels[m.Name]) {
				keep = false
				break
			}
		}
		if keep {
			selected = append(selected, g)
		}
	}
	return selected
}

// StripQuery removes matchers on routing-label keys from a LogQL query string so
// virtual labels such as __sg__ are not forwarded to upstream Loki (which would
// reject an unknown label). It accepts full log or metric queries as well as
// bare `{...}` selectors (match[]).
//
// It returns (newQuery, changed, empty): changed is true when at least one
// matcher was removed; empty is true when stripping left a selector with no
// matchers (i.e. `{}`, which Loki rejects). When the query cannot be parsed or
// nothing is stripped, the original query is returned with changed=false.
func StripQuery(query string, routingKeys map[string]struct{}) (newQuery string, changed bool, empty bool) {
	if len(routingKeys) == 0 || query == "" {
		return query, false, false
	}

	expr, err := syntax.ParseExpr(query)
	if err != nil {
		return query, false, false
	}

	expr.Walk(func(e syntax.Expr) bool {
		me, ok := e.(*syntax.MatchersExpr)
		if !ok {
			return true
		}
		kept := me.Mts[:0]
		for _, m := range me.Mts {
			if _, isRouting := routingKeys[m.Name]; isRouting {
				changed = true
				continue
			}
			kept = append(kept, m)
		}
		me.Mts = kept
		if len(kept) == 0 {
			empty = true
		}
		return true
	})

	if !changed {
		return query, false, false
	}
	return expr.String(), true, empty
}

// StripRequest returns the raw URL query and request body to forward to the
// selected server groups, with routing-key matchers removed from the `query`
// and `match[]` parameters (in whichever of the URL or the form-urlencoded body
// they appear). The original r.URL and bodyBytes are left untouched so the
// caller can still replay them if needed. empty is true when any stripped
// selector was left with no matchers (i.e. `{}`), which upstream Loki rejects.
func StripRequest(r *http.Request, bodyBytes []byte, routingKeys map[string]struct{}) (rawQuery string, body []byte, empty bool) {
	rawQuery = r.URL.RawQuery
	body = bodyBytes

	urlValues := r.URL.Query()
	if changed, e := stripValues(urlValues, routingKeys); changed {
		rawQuery = urlValues.Encode()
		empty = empty || e
	}

	if len(bodyBytes) > 0 && isFormURLEncoded(r) {
		if bodyValues, err := url.ParseQuery(string(bodyBytes)); err == nil {
			if changed, e := stripValues(bodyValues, routingKeys); changed {
				body = []byte(bodyValues.Encode())
				empty = empty || e
			}
		}
	}
	return rawQuery, body, empty
}

// stripValues rewrites the `query` and `match[]` entries of values in place,
// removing routing-key matchers. It reports whether anything changed and
// whether any selector was stripped to an empty `{}`.
func stripValues(values url.Values, routingKeys map[string]struct{}) (changed, empty bool) {
	if q := values.Get("query"); q != "" {
		if nq, c, e := StripQuery(q, routingKeys); c {
			values.Set("query", nq)
			changed = true
			empty = empty || e
		}
	}
	for i, m := range values["match[]"] {
		if nm, c, e := StripQuery(m, routingKeys); c {
			values["match[]"][i] = nm
			changed = true
			empty = empty || e
		}
	}
	return changed, empty
}

// matchersFromQuery extracts the stream-selector matchers from a full LogQL
// query, whether it is a log selector or a metric expression.
func matchersFromQuery(q string) ([]*labels.Matcher, error) {
	expr, err := syntax.ParseExpr(q)
	if err != nil {
		return nil, err
	}
	switch e := expr.(type) {
	case syntax.SampleExpr:
		sel, err := e.Selector()
		if err != nil {
			return nil, err
		}
		return sel.Matchers(), nil
	case syntax.LogSelectorExpr:
		return e.Matchers(), nil
	default:
		return nil, nil
	}
}

// isFormURLEncoded reports whether the request body is form-urlencoded.
func isFormURLEncoded(r *http.Request) bool {
	ct := r.Header.Get("Content-Type")
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	return strings.TrimSpace(ct) == "application/x-www-form-urlencoded"
}
