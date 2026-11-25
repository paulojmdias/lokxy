package proxy

import (
	"bytes"
	"io"
	"net/http"
	"net/url"

	"github.com/grafana/loki/v3/pkg/logql/syntax"
)

// queryFromRequest reads the LogQL query without consuming a form-encoded
// request body that fanoutRequest still needs to forward to each backend.
func queryFromRequest(r *http.Request) string {
	if query := r.URL.Query().Get("query"); query != "" {
		return query
	}
	if r.Body == nil {
		return ""
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return ""
	}
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(body))
	values, err := url.ParseQuery(string(body))
	if err != nil {
		return ""
	}
	return values.Get("query")
}

// isAdditiveVectorQuery reports whether a query matches the supported
// additive-shard subset whose per-shard vector results can be combined by
// addition. This intentionally does not attempt generic LogQL federation.
// Loki evaluates each backend independently, so summing arbitrary vectors
// would be incorrect for max, avg, topk, vector(), and similar expressions.
//
// The OR case supports the common Grafana panel pattern of appending
// label_replace(vector(0), ...) entries to a sum expression to preserve
// zero-valued series.
func isAdditiveVectorQuery(query string) bool {
	expr, err := syntax.ParseExpr(query)
	if err != nil {
		return false
	}
	return isAdditiveExpr(expr)
}

func isAdditiveExpr(expr syntax.Expr) bool {
	switch e := expr.(type) {
	case *syntax.VectorAggregationExpr:
		if e.Operation != syntax.OpTypeSum {
			return false
		}
		rangeExpr, ok := e.Left.(*syntax.RangeAggregationExpr)
		if !ok {
			return false
		}
		switch rangeExpr.Operation {
		case syntax.OpRangeTypeBytes, syntax.OpRangeTypeBytesRate,
			syntax.OpRangeTypeCount, syntax.OpRangeTypeRate,
			syntax.OpRangeTypeRateCounter, syntax.OpRangeTypeSum:
			return true
		default:
			return false
		}
	case *syntax.BinOpExpr:
		return e.Op == syntax.OpTypeOr &&
			((isAdditiveExpr(e.SampleExpr) && isZeroFillExpr(e.RHS)) ||
				(isAdditiveExpr(e.RHS) && isZeroFillExpr(e.SampleExpr)))
	default:
		return false
	}
}

func isZeroFillExpr(expr syntax.Expr) bool {
	switch e := expr.(type) {
	case *syntax.VectorExpr:
		return e.Val == 0
	case *syntax.LabelReplaceExpr:
		vector, ok := e.Left.(*syntax.VectorExpr)
		return ok && vector.Val == 0
	default:
		return false
	}
}
