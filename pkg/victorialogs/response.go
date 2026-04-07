package victorialogs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/VictoriaMetrics-Community/logql-to-logsql/lib/logsql"
)

// parseMode indicates which LogQL parsing stage (| json, | logfmt) was
// present in the original query.
type parseMode int

const (
	parseModeNone   parseMode = iota
	parseModeJSON             // | json
	parseModeLogfmt           // | logfmt
)

// jsonFieldExpr represents a single field expression from "| json".
//
// Examples:
//
//	| json              -> no expressions (extract all top-level keys)
//	| json level        -> {label:"level", path:""}     (top-level key "level")
//	| json sev="level"  -> {label:"sev",   path:"level"} (top-level key "level", output as "sev")
//	| json ip="net.src" -> {label:"ip",    path:"net.src"} (nested path)
type jsonFieldExpr struct {
	label string // output label name
	path  string // dot-separated path; empty means same as label (top-level)
}

// parseConfig carries the detected pipeline parse mode plus optional
// field expressions for selective extraction.
type parseConfig struct {
	mode      parseMode
	jsonExprs []jsonFieldExpr // only used when mode == parseModeJSON; empty = extract all
}

// detectParseConfig inspects the original LogQL query string for parsing
// stage operators (| json, | logfmt) and returns the mode together with
// any field expressions.
//
// It uses simple substring matching which is sufficient because these
// tokens only appear as pipeline stage operators in LogQL.
func detectParseConfig(query string) parseConfig {
	q := strings.ToLower(query)

	// --- | json ---
	if idx := strings.Index(q, "| json"); idx >= 0 {
		after := idx + len("| json")
		if after >= len(q) || q[after] == ' ' || q[after] == '|' || q[after] == '\t' {
			// Extract expression segment between "| json" and the next "|" or end.
			// Use the ORIGINAL query (not lowered) to preserve casing in paths.
			exprStart := idx + len("| json")
			exprSegment := query[exprStart:]
			if pipeIdx := strings.IndexByte(exprSegment, '|'); pipeIdx >= 0 {
				exprSegment = exprSegment[:pipeIdx]
			}
			exprs := parseJSONExprs(strings.TrimSpace(exprSegment))
			return parseConfig{mode: parseModeJSON, jsonExprs: exprs}
		}
	}

	// --- | logfmt ---
	if idx := strings.Index(q, "| logfmt"); idx >= 0 {
		after := idx + len("| logfmt")
		if after >= len(q) || q[after] == ' ' || q[after] == '|' || q[after] == '\t' {
			return parseConfig{mode: parseModeLogfmt}
		}
	}

	return parseConfig{mode: parseModeNone}
}

// parseJSONExprs parses the expression list after "| json".
//
// Formats:
//
//	""                          -> nil  (extract all)
//	"level"                     -> [{label:"level", path:""}]
//	"level, caller"             -> [{label:"level"}, {label:"caller"}]
//	"sev=\"level\""             -> [{label:"sev", path:"level"}]
//	"ip=\"net.src\", level"     -> [{label:"ip", path:"net.src"}, {label:"level"}]
func parseJSONExprs(segment string) []jsonFieldExpr {
	segment = strings.TrimSpace(segment)
	if segment == "" {
		return nil
	}

	var exprs []jsonFieldExpr
	for _, part := range strings.Split(segment, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		if eqIdx := strings.IndexByte(part, '='); eqIdx >= 0 {
			label := strings.TrimSpace(part[:eqIdx])
			path := strings.TrimSpace(part[eqIdx+1:])
			// Strip surrounding quotes from path.
			path = strings.Trim(path, "\"")
			if label != "" {
				exprs = append(exprs, jsonFieldExpr{label: label, path: path})
			}
		} else {
			// Bare field name: extract top-level key by that name.
			exprs = append(exprs, jsonFieldExpr{label: part, path: ""})
		}
	}
	return exprs
}

// TransformResponse converts a VictoriaLogs HTTP response into a
// Loki-compatible HTTP response based on the original Loki API path
// and the query kind.
func TransformResponse(
	lokiPath string,
	queryKind logsql.QueryKind,
	vlogsResp *http.Response,
	streamLabels []string,
	origParams url.Values,
) (*http.Response, error) {
	body, err := io.ReadAll(vlogsResp.Body)
	_ = vlogsResp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("failed to read VictoriaLogs response body: %w", err)
	}

	var result []byte

	switch {
	case isLabelValuesPath(lokiPath):
		result, err = transformFieldValues(body)
	case lokiPath == LokiPathLabels:
		result, err = transformFieldNames(body)
	case lokiPath == LokiPathSeries:
		result, err = transformSeries(body)
	case lokiPath == LokiPathIndexStats:
		result, err = transformIndexStats(body)
	case queryKind == logsql.QueryKindStats && lokiPath == LokiPathQuery:
		result, err = transformStatsInstant(body)
	case queryKind == logsql.QueryKindStats && lokiPath == LokiPathQueryRange:
		result, err = transformStatsRange(body)
	default:
		// Log queries (query and query_range).
		// Detect LogQL parsing stages (| json, | logfmt) in the original
		// query. When present, we emulate the extraction by parsing _msg
		// and adding extracted fields as stream labels, matching Loki's
		// server-side behavior.
		pc := detectParseConfig(origParams.Get("query"))
		result, err = transformLogQuery(body, streamLabels, pc)
	}

	if err != nil {
		return nil, err
	}

	return BuildHTTPResponse(result), nil
}

// BuildHTTPResponse wraps JSON bytes in a synthetic *http.Response
// with status 200 and application/json content type.
func BuildHTTPResponse(body []byte) *http.Response {
	return &http.Response{
		StatusCode:    http.StatusOK,
		Status:        "200 OK",
		Header:        http.Header{"Content-Type": []string{"application/json"}},
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
	}
}

// resolveEvalTime extracts the evaluation timestamp for instant queries
// from the original request parameters. Falls back to current time.
func resolveEvalTime(params url.Values) time.Time {
	// Instant queries use "time" param; Grafana sometimes sends "end".
	for _, key := range []string{"time", "end"} {
		if v := params.Get(key); v != "" {
			if t, err := parseTimeParam(v); err == nil {
				return t
			}
		}
	}
	return time.Now()
}

// parseTimeParam parses a Loki/Prometheus-style time parameter.
// Accepts: RFC3339, RFC3339Nano, or Unix seconds (float).
func parseTimeParam(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		sec := int64(f)
		nsec := int64((f - float64(sec)) * 1e9)
		return time.Unix(sec, nsec), nil
	}
	return time.Time{}, fmt.Errorf("unable to parse time %q", s)
}

// parseVLogsTime parses a VictoriaLogs _time field value.
func parseVLogsTime(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("unable to parse VictoriaLogs time %q", s)
}

// parsePromStyleLabels parses a Prometheus-style label string like
// {hostname="web1",app="nginx"} into a map.
func parsePromStyleLabels(s string) (map[string]string, error) {
	result := make(map[string]string)
	s = strings.TrimSpace(s)
	if s == "" || s == "{}" {
		return result, nil
	}
	if !strings.HasPrefix(s, "{") || !strings.HasSuffix(s, "}") {
		return nil, fmt.Errorf("invalid label string: %q", s)
	}
	s = s[1 : len(s)-1] // Strip { and }.

	for len(s) > 0 {
		s = strings.TrimLeft(s, " ")
		if len(s) == 0 {
			break
		}

		// Key (up to '=').
		eqIdx := strings.IndexByte(s, '=')
		if eqIdx < 0 {
			return nil, fmt.Errorf("missing '=' in label pair: %q", s)
		}
		key := strings.TrimSpace(s[:eqIdx])
		s = s[eqIdx+1:]

		// Value must be quoted.
		if len(s) == 0 || s[0] != '"' {
			return nil, fmt.Errorf("expected quoted value for key %q", key)
		}
		s = s[1:] // Skip opening quote.

		var val strings.Builder
		for len(s) > 0 {
			if s[0] == '\\' && len(s) > 1 {
				val.WriteByte(s[1])
				s = s[2:]
				continue
			}
			if s[0] == '"' {
				s = s[1:] // Skip closing quote.
				break
			}
			val.WriteByte(s[0])
			s = s[1:]
		}

		result[key] = val.String()

		// Skip comma separator.
		s = strings.TrimLeft(s, " ")
		if len(s) > 0 && s[0] == ',' {
			s = s[1:]
		}
	}

	return result, nil
}

// extractStreamLabelsFromLine extracts the Loki stream identity labels
// from a VictoriaLogs JSONL line.
//
// MODE A (static): streamLabelKeys is non-empty -- read those fields
// directly from the line.
//
// MODE B (dynamic): streamLabelKeys is empty -- parse the _stream field.
func extractStreamLabelsFromLine(line map[string]any, streamLabelKeys []string) map[string]string {
	if len(streamLabelKeys) > 0 {
		labels := make(map[string]string, len(streamLabelKeys))
		for _, key := range streamLabelKeys {
			if v, ok := line[key]; ok {
				labels[key] = fmt.Sprint(v)
			}
		}
		return labels
	}

	// Dynamic mode: parse _stream field.
	streamVal, ok := line["_stream"]
	if !ok {
		return map[string]string{}
	}
	str, ok := streamVal.(string)
	if !ok {
		return map[string]string{}
	}
	labels, err := parsePromStyleLabels(str)
	if err != nil {
		return map[string]string{}
	}
	return labels
}

// streamKey produces a deterministic string key from a label set for
// grouping log lines by stream identity.
func streamKey(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(labels[k])
		b.WriteByte(',')
	}
	return b.String()
}

// formatFloat formats a float64 value as a string for Loki responses,
// trimming unnecessary trailing zeros.
func formatFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// wrapQueryResponse builds the standard Loki query response envelope.
func wrapQueryResponse(resultType string, result any) ([]byte, error) {
	resp := map[string]any{
		"status": "success",
		"data": map[string]any{
			"resultType": resultType,
			"result":     result,
			"stats":      map[string]any{},
		},
	}
	return json.Marshal(resp)
}

// wrapDataResponse builds the standard Loki data response envelope
// (used for labels, series).
func wrapDataResponse(data any) ([]byte, error) {
	resp := map[string]any{
		"status": "success",
		"data":   data,
	}
	return json.Marshal(resp)
}
