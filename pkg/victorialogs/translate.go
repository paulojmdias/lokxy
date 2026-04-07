package victorialogs

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/VictoriaMetrics-Community/logql-to-logsql/lib/logsql"
)

// reTimeFilter matches standalone _time:<duration> filters produced by
// logql-to-logsql for range vectors (e.g. count_over_time(...[1m])).
// For stats_query_range, the time windowing is controlled by start/end/step
// URL params, so these inline _time filters must be stripped.
var reTimeFilter = regexp.MustCompile(`\s*_time:\S+`)

// TranslateQuery translates a LogQL query string to LogsQL.
// Returns the QueryInfo containing the translated query and its kind
// (logs vs stats).
//
// Post-processing applied after the upstream library translation:
//   - Strips standalone "" (empty string filter). In LogQL, |= "" is a
//     no-op that matches everything. In LogsQL, "" matches nothing.
//   - Strips _time:<duration> filters for stats queries. The time range
//     is controlled by start/end/step URL params, not by inline filters.
func TranslateQuery(logqlQuery string) (*logsql.QueryInfo, error) {
	qi, err := logsql.TranslateLogQLToLogsQL(logqlQuery)
	if err != nil {
		return nil, err
	}

	qi.LogsQL = sanitizeLogsQL(qi.LogsQL, qi.Kind)
	return qi, nil
}

// sanitizeLogsQL applies post-translation fixups to a LogsQL query.
func sanitizeLogsQL(query string, kind logsql.QueryKind) string {
	// Strip standalone "" (empty string literal) which is a no-op in LogQL
	// but matches nothing in LogsQL.
	query = stripEmptyStringFilter(query)

	// Strip _time:<duration> filters for stats queries. The upstream
	// library adds these from range vector selectors (e.g. [1m]), but
	// for stats_query_range the time windowing comes from URL params.
	if kind == logsql.QueryKindStats {
		query = stripTimeFilter(query)
	}

	// Clean up any resulting double spaces.
	for strings.Contains(query, "  ") {
		query = strings.ReplaceAll(query, "  ", " ")
	}
	return strings.TrimSpace(query)
}

// stripEmptyStringFilter removes standalone "" tokens from a LogsQL query.
// These come from LogQL's |= "" (line contains empty string = match all).
// In LogsQL, "" is a word filter matching the empty string, which returns
// no results.
func stripEmptyStringFilter(query string) string {
	// Replace `""` that appears as a standalone filter token.
	// We need to be careful not to strip "" inside other constructs.
	// A standalone "" is preceded by whitespace (or start) and followed
	// by whitespace (or end).
	result := strings.ReplaceAll(query, ` ""`, "")
	// Handle case where "" is at the start of the query (unlikely but safe).
	if strings.HasPrefix(result, `""`) {
		result = strings.TrimPrefix(result, `""`)
	}
	return result
}

// stripTimeFilter removes _time:<duration> patterns from a LogsQL query.
func stripTimeFilter(query string) string {
	return reTimeFilter.ReplaceAllString(query, "")
}

// DefaultQueryInfo returns a QueryInfo with a wildcard LogsQL query.
// Used for metadata endpoints (labels, series) where no LogQL query
// is provided -- VictoriaLogs interprets "*" as "match all".
func DefaultQueryInfo() *logsql.QueryInfo {
	return &logsql.QueryInfo{Kind: logsql.QueryKindLogs, LogsQL: "*"}
}

// ExtractLogQLQuery extracts the LogQL query string from a Loki-style
// HTTP request. It checks both GET query parameters and POST form body.
// The bodyBytes parameter contains the original request body (already
// read by fanoutRequest).
func ExtractLogQLQuery(r *http.Request, bodyBytes []byte) string {
	// Try GET query parameter first.
	if q := r.URL.Query().Get("query"); q != "" {
		return q
	}

	// Try POST form body. The body may be application/x-www-form-urlencoded
	// (Grafana sends POST for query_range).
	if len(bodyBytes) > 0 {
		values, err := url.ParseQuery(string(bodyBytes))
		if err == nil {
			if q := values.Get("query"); q != "" {
				return q
			}
		}
	}

	return ""
}

// ExtractLabelName extracts the label name from a Loki label values
// path like /loki/api/v1/label/{name}/values.
func ExtractLabelName(path string) string {
	// Path format: /loki/api/v1/label/{name}/values
	const prefix = "/loki/api/v1/label/"
	const suffix = "/values"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return ""
	}
	name := strings.TrimPrefix(path, prefix)
	name = strings.TrimSuffix(name, suffix)
	if name == "" || strings.Contains(name, "/") {
		return ""
	}
	return name
}
