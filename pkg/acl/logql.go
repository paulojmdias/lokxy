package acl

import (
	"net/http"
	"strings"

	"github.com/grafana/loki/v3/pkg/logql/syntax"
	"github.com/prometheus/prometheus/model/labels"
)

// endpoint describes how to extract the LogQL expression(s) carried by a Loki
// API path and whether that path is subject to ACL evaluation.
type endpoint struct {
	// param is the query/form parameter that holds the LogQL expression.
	param string
	// multi is true when the parameter may appear multiple times and each
	// value must be evaluated independently (e.g. /series match[]).
	multi bool
}

// enforcedEndpoints maps the Loki API paths that carry a LogQL expression to
// the parameter holding it. Paths that are absent return only metadata and are
// never evaluated (see the RFC's Logs Drilldown compatibility carve-out).
var enforcedEndpoints = map[string]endpoint{
	"/loki/api/v1/query":       {param: "query"},
	"/loki/api/v1/query_range": {param: "query"},
	"/loki/api/v1/index/stats": {param: "query"},
	"/loki/api/v1/tail":        {param: "expr"},
	"/loki/api/v1/series":      {param: "match[]", multi: true},
}

// lookupEndpoint reports the ACL endpoint for a request path and whether it is
// enforced at all.
func lookupEndpoint(path string) (endpoint, bool) {
	ep, ok := enforcedEndpoints[path]
	return ep, ok
}

// selector is a single LogQL stream selector: the set of matchers of one
// matcher group. A request can yield several selectors (multiple match[]
// values, or a binary expression with independent groups); each is evaluated
// against the rules independently.
type selector struct {
	matchers []*labels.Matcher
}

// parseSelectors parses a LogQL expression and returns its stream-selector
// groups. It uses ParseExprWithoutValidation so that broad queries such as
// {app=~".*"} parse successfully and can be inspected by the policy engine —
// syntax.ParseExpr would reject them before the ACL ever sees them.
func parseSelectors(query string) ([]selector, error) {
	expr, err := syntax.ParseExprWithoutValidation(query)
	if err != nil {
		return nil, err
	}
	groups, err := syntax.MatcherGroups(expr)
	if err != nil {
		return nil, err
	}
	// A query with no matcher groups (e.g. {}) still needs one empty selector
	// so that empty_selector rules can fire on it.
	if len(groups) == 0 {
		return []selector{{}}, nil
	}
	selectors := make([]selector, 0, len(groups))
	for _, g := range groups {
		selectors = append(selectors, selector{matchers: g.Matchers})
	}
	return selectors, nil
}

// find returns the matchers for a given label name.
func (s selector) find(name string) []*labels.Matcher {
	var out []*labels.Matcher
	for _, m := range s.matchers {
		if m.Name == name {
			out = append(out, m)
		}
	}
	return out
}

// isEmpty reports whether the selector has no meaningful stream selector:
// either no matchers at all, or every matcher is a catch-all pattern.
func (s selector) isEmpty() bool {
	if len(s.matchers) == 0 {
		return true
	}
	for _, m := range s.matchers {
		if !isCatchAll(m) {
			return false
		}
	}
	return true
}

// isCatchAll reports whether a matcher imposes no real constraint. It covers the
// common patterns (=~".*", =~".+", their non-greedy and parenthesized variants,
// and !="") but does not attempt general regex-equivalence; exotic catch-alls
// such as =~"[\s\S]*" are not detected.
func isCatchAll(m *labels.Matcher) bool {
	switch m.Type {
	case labels.MatchRegexp: // =~
		v := strings.Trim(m.Value, "()")
		return v == ".*" || v == ".+" || v == ".*?" || v == ".+?"
	case labels.MatchNotEqual: // !=
		return m.Value == ""
	default:
		return false
	}
}

// satisfiesRequire reports whether the selector carries a matcher named
// req.Name whose type is among req.Types (any type when Types is empty).
func (s selector) satisfiesRequire(req RequireSpec) bool {
	return len(filterByType(s.find(req.Name), req.Types)) > 0
}

// conditionMatches reports whether a single condition matches, given the
// selector under evaluation and the request headers.
func conditionMatches(cond MatchCondition, sel selector, header http.Header) bool {
	if cond.EmptySelector {
		return sel.isEmpty()
	}
	if cond.Source == SourceHeader {
		return headerConditionMatches(cond, header)
	}
	return queryConditionMatches(cond, sel)
}

func headerConditionMatches(cond MatchCondition, header http.Header) bool {
	v := header.Get(cond.Name)
	if cond.Absent {
		return v == ""
	}
	if cond.Value == "" {
		return v != ""
	}
	return v == cond.Value
}

func queryConditionMatches(cond MatchCondition, sel selector) bool {
	matchers := filterByType(sel.find(cond.Name), cond.Types)
	if cond.Absent {
		return len(matchers) == 0
	}
	for _, m := range matchers {
		// An empty target value matches on presence alone. Otherwise the
		// condition's value is tested against the matcher: for = this is
		// equality, for =~ the user's regex is evaluated against the value.
		if cond.Value == "" || m.Matches(cond.Value) {
			return true
		}
	}
	return false
}

// filterByType keeps only matchers whose operator is listed in types. An empty
// types slice keeps every matcher.
func filterByType(matchers []*labels.Matcher, types []string) []*labels.Matcher {
	if len(types) == 0 {
		return matchers
	}
	allowed := make(map[string]bool, len(types))
	for _, t := range types {
		allowed[t] = true
	}
	var out []*labels.Matcher
	for _, m := range matchers {
		if allowed[m.Type.String()] {
			out = append(out, m)
		}
	}
	return out
}
