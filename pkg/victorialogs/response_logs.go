package victorialogs

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// streamEntry holds a single log entry before grouping.
type streamEntry struct {
	timestampNano int64
	line          string
}

// streamGroup accumulates log entries for a single stream identity.
type streamGroup struct {
	labels  map[string]string
	entries []streamEntry
}

// transformLogQuery converts VictoriaLogs JSONL log output into a Loki
// streams response.
//
// VictoriaLogs returns one JSON object per line:
//
//	{"_time":"2024-01-01T00:00:00Z","_msg":"log line","hostname":"web1",...}
//
// Loki expects:
//
//	{"status":"success","data":{"resultType":"streams","result":[
//	  {"stream":{"hostname":"web1"},"values":[["1704067200000000000","log line"]]}
//	],"stats":{}}}
//
// When pc.mode is parseModeJSON or parseModeLogfmt, the _msg field is
// parsed and its extracted fields are added as stream labels, emulating
// Loki's server-side behavior. Each unique combination of (stream
// labels + extracted fields) becomes its own stream.
func transformLogQuery(body []byte, streamLabelKeys []string, pc parseConfig) ([]byte, error) {
	groups := make(map[string]*streamGroup)
	// Preserve insertion order for deterministic output.
	var groupOrder []string

	scanner := bufio.NewScanner(bytes.NewReader(body))
	// Increase the default buffer size for potentially large JSONL lines.
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}

		var obj map[string]any
		if err := json.Unmarshal(line, &obj); err != nil {
			// Skip malformed lines rather than failing the whole response.
			continue
		}

		// Extract timestamp.
		timeVal, ok := obj["_time"]
		if !ok {
			continue
		}
		timeStr, ok := timeVal.(string)
		if !ok {
			continue
		}
		t, err := parseVLogsTime(timeStr)
		if err != nil {
			continue
		}

		// Extract log line.
		msg := ""
		if v, ok := obj["_msg"]; ok {
			msg = fmt.Sprint(v)
		}

		// Extract stream labels.
		labels := extractStreamLabelsFromLine(obj, streamLabelKeys)

		// Emulate pipeline extraction stages.
		if msg != "" {
			switch pc.mode {
			case parseModeJSON:
				extractJSONFields(msg, labels, pc.jsonExprs)
			case parseModeLogfmt:
				extractLogfmtFields(msg, labels)
			}
		}

		key := streamKey(labels)

		group, exists := groups[key]
		if !exists {
			group = &streamGroup{labels: labels}
			groups[key] = group
			groupOrder = append(groupOrder, key)
		}

		group.entries = append(group.entries, streamEntry{
			timestampNano: t.UnixNano(),
			line:          msg,
		})
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error scanning VictoriaLogs JSONL: %w", err)
	}

	// Build the Loki streams result.
	result := make([]map[string]any, 0, len(groups))
	for _, key := range groupOrder {
		group := groups[key]

		// Sort entries by timestamp (ascending).
		sort.Slice(group.entries, func(i, j int) bool {
			return group.entries[i].timestampNano < group.entries[j].timestampNano
		})

		values := make([][]string, len(group.entries))
		for i, entry := range group.entries {
			values[i] = []string{
				strconv.FormatInt(entry.timestampNano, 10),
				entry.line,
			}
		}

		result = append(result, map[string]any{
			"stream": group.labels,
			"values": values,
		})
	}

	return wrapQueryResponse("streams", result)
}

// ---------------------------------------------------------------------------
// | json extraction
// ---------------------------------------------------------------------------

// extractJSONFields parses msg as JSON and adds fields to the labels
// map, emulating Loki's | json pipeline stage.
//
// When exprs is empty, ALL top-level keys are extracted.
// When exprs is non-empty, only the specified fields are extracted:
//   - {label:"level", path:""}       -> labels["level"] = parsed["level"]
//   - {label:"sev", path:"level"}    -> labels["sev"]   = parsed["level"]
//   - {label:"ip", path:"net.src"}   -> labels["ip"]    = navigateJSONPath(parsed, "net.src")
//
// Value conversion rules (matching Loki):
//   - string       -> as-is
//   - number       -> decimal string (no scientific notation)
//   - bool         -> "true" / "false"
//   - null         -> "" (empty string)
//   - object/array -> JSON-stringified
//
// Keys that already exist in labels are NOT overwritten (stream labels
// take precedence, matching Loki behavior).
//
// If _msg is not valid JSON, no fields are extracted (silent no-op).
func extractJSONFields(msg string, labels map[string]string, exprs []jsonFieldExpr) {
	var parsed map[string]any
	if err := json.Unmarshal([]byte(msg), &parsed); err != nil {
		return // Not valid JSON -- skip silently.
	}

	if len(exprs) == 0 {
		// Extract all top-level keys.
		for k, v := range parsed {
			if _, exists := labels[k]; exists {
				continue
			}
			labels[k] = anyToString(v)
		}
		return
	}

	// Selective extraction with optional nested path navigation.
	for _, expr := range exprs {
		if _, exists := labels[expr.label]; exists {
			continue
		}
		lookupPath := expr.path
		if lookupPath == "" {
			lookupPath = expr.label
		}
		if v, ok := navigateJSONPath(parsed, lookupPath); ok {
			labels[expr.label] = anyToString(v)
		}
	}
}

// navigateJSONPath traverses a parsed JSON object using a dot-separated
// path. Returns the value and true if found, or (nil, false) if any
// segment is missing or not an object.
//
// Example: navigateJSONPath(obj, "net.src") -> obj["net"]["src"]
func navigateJSONPath(obj map[string]any, path string) (any, bool) {
	parts := strings.Split(path, ".")
	var current any = obj

	for _, part := range parts {
		m, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = m[part]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

// anyToString converts a JSON-decoded value to its string representation,
// matching Loki's | json extraction behavior.
func anyToString(v any) string {
	switch val := v.(type) {
	case string:
		return val
	case float64:
		return strconv.FormatFloat(val, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(val)
	case nil:
		return ""
	default:
		// Objects, arrays -- JSON-stringify.
		b, err := json.Marshal(val)
		if err != nil {
			return fmt.Sprint(val)
		}
		return string(b)
	}
}

// ---------------------------------------------------------------------------
// | logfmt extraction
// ---------------------------------------------------------------------------

// extractLogfmtFields parses msg as logfmt and adds extracted key/value
// pairs to the labels map, emulating Loki's | logfmt pipeline stage.
//
// Logfmt format: key=value key2="quoted value" key3=
//
// Keys that already exist in labels are NOT overwritten (stream labels
// take precedence). If _msg is not valid logfmt, partially extracted
// fields are still added (best-effort).
func extractLogfmtFields(msg string, labels map[string]string) {
	s := msg
	for len(s) > 0 {
		// Skip leading whitespace.
		s = strings.TrimLeft(s, " \t")
		if len(s) == 0 {
			break
		}

		// Find key (up to '=' or whitespace).
		var key string
		eqIdx := strings.IndexByte(s, '=')
		spIdx := strings.IndexAny(s, " \t")

		if eqIdx < 0 {
			// No '=' found -- rest of string is a bare key with no value.
			// In logfmt, bare keys (no =) are valid with empty value.
			key = s
			s = ""
		} else if spIdx >= 0 && spIdx < eqIdx {
			// Space before '=' -- bare key with no value.
			key = s[:spIdx]
			s = s[spIdx:]
		} else {
			key = s[:eqIdx]
			s = s[eqIdx+1:]
		}

		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}

		// Parse value.
		var val string
		if len(s) > 0 && s[0] == '"' {
			// Quoted value -- find matching close quote.
			val, s = parseLogfmtQuotedValue(s)
		} else if eqIdx >= 0 && !(spIdx >= 0 && spIdx < eqIdx) {
			// Unquoted value -- up to next space or end.
			endIdx := strings.IndexAny(s, " \t")
			if endIdx < 0 {
				val = s
				s = ""
			} else {
				val = s[:endIdx]
				s = s[endIdx:]
			}
		}
		// else: bare key with no '=' -> val stays ""

		if _, exists := labels[key]; !exists {
			labels[key] = val
		}
	}
}

// parseLogfmtQuotedValue parses a double-quoted logfmt value starting
// at s[0]=='"'. Returns the unquoted value and the remaining string
// after the closing quote.
func parseLogfmtQuotedValue(s string) (string, string) {
	s = s[1:] // Skip opening quote.
	var b strings.Builder
	for len(s) > 0 {
		if s[0] == '\\' && len(s) > 1 {
			b.WriteByte(s[1])
			s = s[2:]
			continue
		}
		if s[0] == '"' {
			s = s[1:] // Skip closing quote.
			return b.String(), s
		}
		b.WriteByte(s[0])
		s = s[1:]
	}
	// Unterminated quote -- return what we have.
	return b.String(), ""
}
