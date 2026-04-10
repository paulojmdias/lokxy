package proxy

import "strings"

// rewriteMode controls how metadata matchers are handled after extraction
// from the stream selector.
type rewriteMode int

const (
	// rewriteMoveToPipeline moves metadata matchers into LogQL pipeline
	// label-filter stages: {real="x", meta="y"} -> {real="x"} | meta="y"
	// Used for /query and /query_range which support full LogQL.
	rewriteMoveToPipeline rewriteMode = iota

	// rewriteStrip removes metadata matchers entirely:
	// {real="x", meta="y"} -> {real="x"}
	// Used for endpoints that only accept stream selectors (/labels,
	// /label/{name}/values, /index/volume, etc.).
	rewriteStrip
)

// rewriteMetadataQuery inspects a LogQL query string and extracts matchers
// for configured metadata fields from the stream selector {...}.
//
// Depending on mode:
//   - rewriteMoveToPipeline: extracted matchers become pipeline label-filter
//     stages inserted immediately after the stream selector.
//   - rewriteStrip: extracted matchers are dropped entirely.
//
// The query is returned unchanged when:
//   - metadataFields is empty (we cannot identify which matchers to rewrite)
//   - no metadata matchers are found in the stream selector
//   - removing metadata matchers would leave the stream selector empty
func rewriteMetadataQuery(query string, metadataFields []string, mode rewriteMode) string {
	if len(metadataFields) == 0 || query == "" {
		return query
	}

	mfSet := make(map[string]struct{}, len(metadataFields))
	for _, f := range metadataFields {
		mfSet[f] = struct{}{}
	}

	// Locate the stream selector: first '{' ... matching '}'.
	selStart, selEnd := findSelector(query)
	if selStart < 0 {
		return query
	}

	inner := query[selStart+1 : selEnd-1] // content between { and }
	matchers := splitMatchers(inner)

	var keep, moved []string
	for _, m := range matchers {
		label := matcherLabel(m)
		if _, ok := mfSet[label]; ok {
			moved = append(moved, strings.TrimSpace(m))
		} else {
			keep = append(keep, strings.TrimSpace(m))
		}
	}

	if len(moved) == 0 {
		return query // nothing to rewrite
	}
	if len(keep) == 0 {
		return query // would leave empty selector
	}

	// Reconstruct the query.
	var b strings.Builder
	b.WriteString(query[:selStart])
	b.WriteByte('{')
	b.WriteString(strings.Join(keep, ", "))
	b.WriteByte('}')

	if mode == rewriteMoveToPipeline {
		for _, m := range moved {
			b.WriteString(" | ")
			b.WriteString(m)
		}
	}

	b.WriteString(query[selEnd:])
	return b.String()
}

// findSelector returns the byte offsets of the first '{' and one past the
// matching '}', properly skipping over quoted strings. Returns (-1, -1)
// when no valid selector is found.
func findSelector(query string) (int, int) {
	start := -1
	i := 0
	for i < len(query) {
		switch query[i] {
		case '{':
			if start < 0 {
				start = i
			}
		case '}':
			if start >= 0 {
				return start, i + 1
			}
		case '"':
			i = skipDoubleQuoted(query, i)
		case '`':
			i = skipBacktickQuoted(query, i)
		}
		i++
	}
	return -1, -1
}

// splitMatchers splits the content between { and } into individual matchers,
// respecting quoted strings that may contain commas.
func splitMatchers(inner string) []string {
	var matchers []string
	var cur strings.Builder
	i := 0
	for i < len(inner) {
		ch := inner[i]
		switch ch {
		case ',':
			if s := strings.TrimSpace(cur.String()); s != "" {
				matchers = append(matchers, s)
			}
			cur.Reset()
			i++
		case '"':
			end := skipDoubleQuoted(inner, i)
			cur.WriteString(inner[i : end+1])
			i = end + 1
		case '`':
			end := skipBacktickQuoted(inner, i)
			cur.WriteString(inner[i : end+1])
			i = end + 1
		default:
			cur.WriteByte(ch)
			i++
		}
	}
	if s := strings.TrimSpace(cur.String()); s != "" {
		matchers = append(matchers, s)
	}
	return matchers
}

// matcherLabel extracts the label name from a matcher like `label="value"`.
func matcherLabel(m string) string {
	m = strings.TrimSpace(m)
	for i := 0; i < len(m); i++ {
		if m[i] == '=' || m[i] == '!' {
			return strings.TrimSpace(m[:i])
		}
	}
	return m
}

// skipDoubleQuoted advances past a double-quoted string starting at pos,
// handling backslash escapes. Returns the index of the closing '"'.
func skipDoubleQuoted(s string, pos int) int {
	i := pos + 1 // skip opening '"'
	for i < len(s) {
		if s[i] == '\\' && i+1 < len(s) {
			i += 2 // skip escaped character
			continue
		}
		if s[i] == '"' {
			return i
		}
		i++
	}
	return len(s) - 1 // unterminated; best effort
}

// skipBacktickQuoted advances past a backtick-quoted string starting at pos.
// Returns the index of the closing '`'.
func skipBacktickQuoted(s string, pos int) int {
	i := pos + 1 // skip opening '`'
	for i < len(s) {
		if s[i] == '`' {
			return i
		}
		i++
	}
	return len(s) - 1 // unterminated; best effort
}
