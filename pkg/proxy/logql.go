package proxy

import (
	"fmt"
	"strconv"
	"time"

	"github.com/grafana/loki/v3/pkg/logql/syntax"
	"github.com/prometheus/common/model"
)

// rewriteRangeVector parses a LogQL expression and rewrites the range vector
// interval if it is smaller than minInterval. Returns the (possibly rewritten)
// query string and whether a rewrite occurred.
//
// Example:
//
//	rewriteRangeVector(`sum by (level) (count_over_time({app="foo"}[5s]))`, 1*time.Minute)
//	=> `sum by (level)(count_over_time({app="foo"}[1m]))`, true, nil
func rewriteRangeVector(query string, minInterval time.Duration) (string, bool, error) {
	expr, err := syntax.ParseExpr(query)
	if err != nil {
		return query, false, fmt.Errorf("failed to parse LogQL expression: %w", err)
	}

	rewritten := false

	// Walk the AST to find RangeAggregationExpr nodes.
	// For logvolhist queries the top-level is typically a VectorAggregationExpr
	// wrapping a RangeAggregationExpr, but we handle arbitrary nesting.
	expr.Walk(func(e syntax.Expr) bool {
		rangeAgg, ok := e.(*syntax.RangeAggregationExpr)
		if !ok {
			return true // continue walking
		}
		if rangeAgg.Left == nil {
			return true
		}
		if rangeAgg.Left.Interval < minInterval {
			rangeAgg.Left.Interval = minInterval
			rewritten = true
		}
		return true
	})

	if rewritten {
		return expr.String(), true, nil
	}
	return query, false, nil
}

// queryHasLineFilters returns true if the LogQL expression contains any
// non-trivial line filter stages (|=, !=, |~, !~). Used to decide whether
// to redirect to the Volume API (which cannot evaluate line filters).
//
// Grafana adds |= “ (empty string match) by default to logvolhist queries.
// Empty-match filters are no-ops and are ignored here so that these queries
// remain eligible for Volume API redirect.
func queryHasLineFilters(query string) (bool, error) {
	expr, err := syntax.ParseExpr(query)
	if err != nil {
		return false, fmt.Errorf("failed to parse LogQL expression: %w", err)
	}

	filters := syntax.ExtractLineFilters(expr)
	for _, f := range filters {
		if f.Match != "" {
			return true, nil
		}
	}
	return false, nil
}

// extractStreamSelector parses a LogQL expression and returns the stream selector
// string (e.g., `{app="foo", env="prod"}`). Returns error if no selector is found.
func extractStreamSelector(query string) (string, error) {
	expr, err := syntax.ParseExpr(query)
	if err != nil {
		return "", fmt.Errorf("failed to parse LogQL expression: %w", err)
	}

	var selector string
	expr.Walk(func(e syntax.Expr) bool {
		rangeAgg, ok := e.(*syntax.RangeAggregationExpr)
		if !ok {
			return true
		}
		if rangeAgg.Left != nil && rangeAgg.Left.Left != nil {
			matchers := rangeAgg.Left.Left.Matchers()
			if len(matchers) > 0 {
				selector = "{"
				for i, m := range matchers {
					if i > 0 {
						selector += ", "
					}
					selector += m.String()
				}
				selector += "}"
			}
		}
		return selector == "" // stop walking once we found a selector
	})

	if selector == "" {
		return "", fmt.Errorf("no stream selector found in query")
	}
	return selector, nil
}

// parseStep parses a step string into a time.Duration.
// Handles both Prometheus duration format ("1m", "1h", "30s") and bare
// seconds as sent by Grafana ("5", "60", "3600").
func parseStep(s string) (time.Duration, error) {
	// Try Prometheus duration format first (handles "1m", "1h", "30s", etc.)
	if d, err := model.ParseDuration(s); err == nil {
		return time.Duration(d), nil
	}
	// Fall back to interpreting as seconds (Grafana sends "5", "60", "3600")
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("cannot parse step %q: %w", s, err)
	}
	return time.Duration(f * float64(time.Second)), nil
}
