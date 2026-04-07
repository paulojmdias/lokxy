package victorialogs

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/VictoriaMetrics-Community/logql-to-logsql/lib/logsql"
	"github.com/stretchr/testify/require"
)

// --- Helper ---

func makeVLogsResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/x-ndjson"}},
		Body:       io.NopCloser(bytes.NewReader([]byte(body))),
	}
}

// --- Log queries (streams) ---

func TestTransformResponse_LogQuery_SingleStream(t *testing.T) {
	body := strings.Join([]string{
		`{"_time":"2024-01-01T00:00:00Z","_msg":"line 1","hostname":"web1","app":"nginx"}`,
		`{"_time":"2024-01-01T00:00:01Z","_msg":"line 2","hostname":"web1","app":"nginx"}`,
	}, "\n")

	resp, err := TransformResponse(
		LokiPathQueryRange, logsql.QueryKindLogs,
		makeVLogsResponse(body),
		[]string{"hostname", "app"},
		url.Values{},
	)
	require.NoError(t, err)
	require.NotNil(t, resp)

	result := readJSON(t, resp)
	require.Equal(t, "success", result["status"])

	data := result["data"].(map[string]any)
	require.Equal(t, "streams", data["resultType"])

	streams := data["result"].([]any)
	require.Len(t, streams, 1)

	stream := streams[0].(map[string]any)
	labels := stream["stream"].(map[string]any)
	require.Equal(t, "web1", labels["hostname"])
	require.Equal(t, "nginx", labels["app"])

	values := stream["values"].([]any)
	require.Len(t, values, 2)

	// Verify timestamp is nanoseconds string.
	firstEntry := values[0].([]any)
	require.Equal(t, "1704067200000000000", firstEntry[0])
	require.Equal(t, "line 1", firstEntry[1])
}

func TestTransformResponse_LogQuery_MultipleStreams(t *testing.T) {
	body := strings.Join([]string{
		`{"_time":"2024-01-01T00:00:00Z","_msg":"line A","hostname":"web1"}`,
		`{"_time":"2024-01-01T00:00:01Z","_msg":"line B","hostname":"web2"}`,
		`{"_time":"2024-01-01T00:00:02Z","_msg":"line C","hostname":"web1"}`,
	}, "\n")

	resp, err := TransformResponse(
		LokiPathQueryRange, logsql.QueryKindLogs,
		makeVLogsResponse(body),
		[]string{"hostname"},
		url.Values{},
	)
	require.NoError(t, err)

	result := readJSON(t, resp)
	data := result["data"].(map[string]any)
	streams := data["result"].([]any)
	require.Len(t, streams, 2)
}

func TestTransformResponse_LogQuery_DynamicStreamLabels(t *testing.T) {
	body := `{"_time":"2024-01-01T00:00:00Z","_msg":"hello","_stream":"{hostname=\"web1\",app=\"nginx\"}"}`

	resp, err := TransformResponse(
		LokiPathQueryRange, logsql.QueryKindLogs,
		makeVLogsResponse(body),
		nil, // dynamic mode
		url.Values{},
	)
	require.NoError(t, err)

	result := readJSON(t, resp)
	data := result["data"].(map[string]any)
	streams := data["result"].([]any)
	require.Len(t, streams, 1)

	stream := streams[0].(map[string]any)
	labels := stream["stream"].(map[string]any)
	require.Equal(t, "web1", labels["hostname"])
	require.Equal(t, "nginx", labels["app"])
}

func TestTransformResponse_LogQuery_Empty(t *testing.T) {
	resp, err := TransformResponse(
		LokiPathQueryRange, logsql.QueryKindLogs,
		makeVLogsResponse(""),
		nil,
		url.Values{},
	)
	require.NoError(t, err)

	result := readJSON(t, resp)
	data := result["data"].(map[string]any)
	streams := data["result"].([]any)
	require.Len(t, streams, 0)
}

func TestTransformResponse_LogQuery_MalformedLines(t *testing.T) {
	body := strings.Join([]string{
		`not json`,
		`{"_time":"2024-01-01T00:00:00Z","_msg":"valid","hostname":"web1"}`,
		`{"bad":`,
	}, "\n")

	resp, err := TransformResponse(
		LokiPathQueryRange, logsql.QueryKindLogs,
		makeVLogsResponse(body),
		[]string{"hostname"},
		url.Values{},
	)
	require.NoError(t, err)

	result := readJSON(t, resp)
	data := result["data"].(map[string]any)
	streams := data["result"].([]any)
	require.Len(t, streams, 1) // Only the valid line.
}

func TestTransformResponse_LogQuery_SortedByTimestamp(t *testing.T) {
	body := strings.Join([]string{
		`{"_time":"2024-01-01T00:00:02Z","_msg":"third","hostname":"web1"}`,
		`{"_time":"2024-01-01T00:00:00Z","_msg":"first","hostname":"web1"}`,
		`{"_time":"2024-01-01T00:00:01Z","_msg":"second","hostname":"web1"}`,
	}, "\n")

	resp, err := TransformResponse(
		LokiPathQueryRange, logsql.QueryKindLogs,
		makeVLogsResponse(body),
		[]string{"hostname"},
		url.Values{},
	)
	require.NoError(t, err)

	result := readJSON(t, resp)
	data := result["data"].(map[string]any)
	streams := data["result"].([]any)
	require.Len(t, streams, 1)

	values := streams[0].(map[string]any)["values"].([]any)
	require.Equal(t, "first", values[0].([]any)[1])
	require.Equal(t, "second", values[1].([]any)[1])
	require.Equal(t, "third", values[2].([]any)[1])
}

// --- Stats instant (vector) ---

func TestTransformResponse_StatsInstant(t *testing.T) {
	// VictoriaLogs stats_query returns Prometheus-compatible vector format.
	body := `{"status":"success","data":{"resultType":"vector","result":[{"metric":{"hostname":"web1"},"value":[1704067200.000,"42"]},{"metric":{"hostname":"web2"},"value":[1704067200.000,"7"]}]}}`

	resp, err := TransformResponse(
		LokiPathQuery, logsql.QueryKindStats,
		makeVLogsResponse(body),
		nil,
		url.Values{"time": []string{"1704067200"}},
	)
	require.NoError(t, err)

	result := readJSON(t, resp)
	data := result["data"].(map[string]any)
	require.Equal(t, "vector", data["resultType"])

	vectors := data["result"].([]any)
	require.Len(t, vectors, 2)

	v0 := vectors[0].(map[string]any)
	metric := v0["metric"].(map[string]any)
	require.Equal(t, "web1", metric["hostname"])

	value := v0["value"].([]any)
	// Timestamp is float seconds.
	ts := value[0].(float64)
	require.InDelta(t, 1704067200.0, ts, 0.001)
	require.Equal(t, "42", value[1])

	// Ensure "stats" field was added.
	require.NotNil(t, data["stats"])
}

func TestTransformResponse_StatsInstant_Empty(t *testing.T) {
	body := `{"status":"success","data":{"resultType":"vector","result":[]}}`

	resp, err := TransformResponse(
		LokiPathQuery, logsql.QueryKindStats,
		makeVLogsResponse(body),
		nil,
		url.Values{},
	)
	require.NoError(t, err)

	result := readJSON(t, resp)
	data := result["data"].(map[string]any)
	require.Equal(t, "vector", data["resultType"])
	vectors := data["result"].([]any)
	require.Len(t, vectors, 0)
}

// --- Stats range (matrix) ---

func TestTransformResponse_StatsRange(t *testing.T) {
	// VictoriaLogs stats_query_range returns Prometheus-compatible matrix format.
	body := `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"hostname":"web1"},"values":[[1704067200,"10"],[1704070800,"15"]]}]}}`

	resp, err := TransformResponse(
		LokiPathQueryRange, logsql.QueryKindStats,
		makeVLogsResponse(body),
		nil,
		url.Values{},
	)
	require.NoError(t, err)

	result := readJSON(t, resp)
	data := result["data"].(map[string]any)
	require.Equal(t, "matrix", data["resultType"])

	series := data["result"].([]any)
	require.Len(t, series, 1)

	s0 := series[0].(map[string]any)
	metric := s0["metric"].(map[string]any)
	require.Equal(t, "web1", metric["hostname"])

	values := s0["values"].([]any)
	require.Len(t, values, 2)

	v0 := values[0].([]any)
	require.InDelta(t, 1704067200.0, v0[0].(float64), 0.001)
	require.Equal(t, "10", v0[1])

	v1 := values[1].([]any)
	require.InDelta(t, 1704070800.0, v1[0].(float64), 0.001)
	require.Equal(t, "15", v1[1])

	// Ensure "stats" field was added.
	require.NotNil(t, data["stats"])
}

// --- Index stats ---

func TestTransformResponse_IndexStats(t *testing.T) {
	// The proxy sends a multi-stats pipe to VictoriaLogs stats_query.
	// Each stat alias becomes a separate vector entry with __name__ label.
	body := `{"status":"success","data":{"resultType":"vector","result":[{"metric":{"__name__":"entries"},"value":[1704067200.000,"5000"]},{"metric":{"__name__":"streams"},"value":[1704067200.000,"42"]},{"metric":{"__name__":"bytes"},"value":[1704067200.000,"1234567"]}]}}`

	resp, err := TransformResponse(
		LokiPathIndexStats, logsql.QueryKindLogs,
		makeVLogsResponse(body),
		nil,
		url.Values{},
	)
	require.NoError(t, err)

	result := readJSON(t, resp)
	require.Equal(t, float64(42), result["streams"])
	require.Equal(t, float64(0), result["chunks"])
	require.Equal(t, float64(1234567), result["bytes"])
	require.Equal(t, float64(5000), result["entries"])
}

func TestTransformResponse_IndexStats_Empty(t *testing.T) {
	// Empty response (no results) should gracefully return zeros.
	body := `{"status":"success","data":{"resultType":"vector","result":[]}}`

	resp, err := TransformResponse(
		LokiPathIndexStats, logsql.QueryKindLogs,
		makeVLogsResponse(body),
		nil,
		url.Values{},
	)
	require.NoError(t, err)

	result := readJSON(t, resp)
	require.Equal(t, float64(0), result["streams"])
	require.Equal(t, float64(0), result["chunks"])
	require.Equal(t, float64(0), result["bytes"])
	require.Equal(t, float64(0), result["entries"])
}

func TestTransformResponse_IndexStats_MalformedBody(t *testing.T) {
	// Malformed body should gracefully return zeros.
	resp, err := TransformResponse(
		LokiPathIndexStats, logsql.QueryKindLogs,
		makeVLogsResponse("not json"),
		nil,
		url.Values{},
	)
	require.NoError(t, err)

	result := readJSON(t, resp)
	require.Equal(t, float64(0), result["entries"])
}

func TestTransformResponse_IndexStats_FloatValue(t *testing.T) {
	// Values might come as float strings like "5000.0".
	body := `{"status":"success","data":{"resultType":"vector","result":[{"metric":{"__name__":"entries"},"value":[1704067200.000,"5000.0"]},{"metric":{"__name__":"streams"},"value":[1704067200.000,"42.0"]},{"metric":{"__name__":"bytes"},"value":[1704067200.000,"1234567.0"]}]}}`

	resp, err := TransformResponse(
		LokiPathIndexStats, logsql.QueryKindLogs,
		makeVLogsResponse(body),
		nil,
		url.Values{},
	)
	require.NoError(t, err)

	result := readJSON(t, resp)
	require.Equal(t, float64(5000), result["entries"])
	require.Equal(t, float64(42), result["streams"])
	require.Equal(t, float64(1234567), result["bytes"])
}

// --- extractNamedVectorValues ---

func TestExtractNamedVectorValues(t *testing.T) {
	resp := map[string]any{
		"data": map[string]any{
			"resultType": "vector",
			"result": []any{
				map[string]any{
					"metric": map[string]any{"__name__": "entries"},
					"value":  []any{1704067200.0, "5000"},
				},
				map[string]any{
					"metric": map[string]any{"__name__": "streams"},
					"value":  []any{1704067200.0, "42"},
				},
				map[string]any{
					"metric": map[string]any{"__name__": "bytes"},
					"value":  []any{1704067200.0, "1234567"},
				},
			},
		},
	}

	vals := extractNamedVectorValues(resp)
	require.Equal(t, int64(5000), vals["entries"])
	require.Equal(t, int64(42), vals["streams"])
	require.Equal(t, int64(1234567), vals["bytes"])
}

func TestExtractNamedVectorValues_PartialResults(t *testing.T) {
	// Only "entries" present -- missing stats should default to zero.
	resp := map[string]any{
		"data": map[string]any{
			"resultType": "vector",
			"result": []any{
				map[string]any{
					"metric": map[string]any{"__name__": "entries"},
					"value":  []any{1704067200.0, "100"},
				},
			},
		},
	}

	vals := extractNamedVectorValues(resp)
	require.Equal(t, int64(100), vals["entries"])
	require.Equal(t, int64(0), vals["streams"])
	require.Equal(t, int64(0), vals["bytes"])
}

func TestExtractNamedVectorValues_EmptyResult(t *testing.T) {
	resp := map[string]any{
		"data": map[string]any{
			"resultType": "vector",
			"result":     []any{},
		},
	}

	vals := extractNamedVectorValues(resp)
	require.Len(t, vals, 0)
}

// --- Labels (field_names) ---

func TestTransformResponse_Labels(t *testing.T) {
	// VictoriaLogs field_names returns JSON with values array.
	body := `{"values":[{"value":"hostname","hits":1000},{"value":"app","hits":500},{"value":"_msg","hits":200}]}`

	resp, err := TransformResponse(
		LokiPathLabels, logsql.QueryKindLogs,
		makeVLogsResponse(body),
		nil,
		url.Values{},
	)
	require.NoError(t, err)

	result := readJSON(t, resp)
	require.Equal(t, "success", result["status"])

	data := result["data"].([]any)
	require.Len(t, data, 3)
	// Should be sorted alphabetically.
	require.Equal(t, "_msg", data[0])
	require.Equal(t, "app", data[1])
	require.Equal(t, "hostname", data[2])
}

func TestTransformResponse_Labels_Empty(t *testing.T) {
	resp, err := TransformResponse(
		LokiPathLabels, logsql.QueryKindLogs,
		makeVLogsResponse(""),
		nil,
		url.Values{},
	)
	require.NoError(t, err)

	result := readJSON(t, resp)
	require.Equal(t, "success", result["status"])
	data := result["data"].([]any)
	require.Len(t, data, 0)
}

// --- Label values (field_values) ---

func TestTransformResponse_LabelValues(t *testing.T) {
	// VictoriaLogs field_values returns JSON with values array.
	body := `{"values":[{"value":"web2","hits":300},{"value":"web1","hits":500}]}`

	resp, err := TransformResponse(
		"/loki/api/v1/label/hostname/values", logsql.QueryKindLogs,
		makeVLogsResponse(body),
		nil,
		url.Values{},
	)
	require.NoError(t, err)

	result := readJSON(t, resp)
	require.Equal(t, "success", result["status"])

	data := result["data"].([]any)
	require.Len(t, data, 2)
	// Sorted.
	require.Equal(t, "web1", data[0])
	require.Equal(t, "web2", data[1])
}

// --- Series (streams) ---

func TestTransformResponse_Series(t *testing.T) {
	// VictoriaLogs streams endpoint returns JSON with values array.
	body := `{"values":[{"value":"{hostname=\"web1\",app=\"nginx\"}","hits":1000},{"value":"{hostname=\"web2\",app=\"redis\"}","hits":500}]}`

	resp, err := TransformResponse(
		LokiPathSeries, logsql.QueryKindLogs,
		makeVLogsResponse(body),
		nil,
		url.Values{},
	)
	require.NoError(t, err)

	result := readJSON(t, resp)
	require.Equal(t, "success", result["status"])

	data := result["data"].([]any)
	require.Len(t, data, 2)

	s0 := data[0].(map[string]any)
	require.Equal(t, "web1", s0["hostname"])
	require.Equal(t, "nginx", s0["app"])

	s1 := data[1].(map[string]any)
	require.Equal(t, "web2", s1["hostname"])
	require.Equal(t, "redis", s1["app"])
}

func TestTransformResponse_Series_Empty(t *testing.T) {
	resp, err := TransformResponse(
		LokiPathSeries, logsql.QueryKindLogs,
		makeVLogsResponse(""),
		nil,
		url.Values{},
	)
	require.NoError(t, err)

	result := readJSON(t, resp)
	data := result["data"].([]any)
	require.Len(t, data, 0)
}

// --- Internal helpers ---

func TestParsePromStyleLabels(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    map[string]string
		wantErr bool
	}{
		{
			name:  "simple",
			input: `{hostname="web1",app="nginx"}`,
			want:  map[string]string{"hostname": "web1", "app": "nginx"},
		},
		{
			name:  "empty",
			input: `{}`,
			want:  map[string]string{},
		},
		{
			name:  "blank string",
			input: "",
			want:  map[string]string{},
		},
		{
			name:  "single label",
			input: `{foo="bar"}`,
			want:  map[string]string{"foo": "bar"},
		},
		{
			name:  "escaped quote in value",
			input: `{msg="say \"hello\""}`,
			want:  map[string]string{"msg": `say "hello"`},
		},
		{
			name:    "no braces",
			input:   `hostname="web1"`,
			wantErr: true,
		},
		{
			name:    "missing equals",
			input:   `{hostname}`,
			wantErr: true,
		},
		{
			name:    "unquoted value",
			input:   `{hostname=web1}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parsePromStyleLabels(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestStreamKey_Deterministic(t *testing.T) {
	labels1 := map[string]string{"app": "nginx", "hostname": "web1"}
	labels2 := map[string]string{"hostname": "web1", "app": "nginx"}
	require.Equal(t, streamKey(labels1), streamKey(labels2))
}

func TestStreamKey_DifferentLabels(t *testing.T) {
	labels1 := map[string]string{"app": "nginx"}
	labels2 := map[string]string{"app": "redis"}
	require.NotEqual(t, streamKey(labels1), streamKey(labels2))
}

func TestResolveEvalTime_TimeParam(t *testing.T) {
	params := url.Values{"time": []string{"1704067200"}}
	got := resolveEvalTime(params)
	require.Equal(t, int64(1704067200), got.Unix())
}

func TestResolveEvalTime_EndParam(t *testing.T) {
	params := url.Values{"end": []string{"1704067200"}}
	got := resolveEvalTime(params)
	require.Equal(t, int64(1704067200), got.Unix())
}

func TestResolveEvalTime_RFC3339(t *testing.T) {
	params := url.Values{"time": []string{"2024-01-01T00:00:00Z"}}
	got := resolveEvalTime(params)
	require.Equal(t, int64(1704067200), got.Unix())
}

func TestResolveEvalTime_FallbackToNow(t *testing.T) {
	params := url.Values{}
	before := time.Now()
	got := resolveEvalTime(params)
	after := time.Now()
	require.True(t, !got.Before(before) && !got.After(after))
}

func TestFormatFloat(t *testing.T) {
	require.Equal(t, "42", formatFloat(42.0))
	require.Equal(t, "3.14", formatFloat(3.14))
	require.Equal(t, "0", formatFloat(0.0))
	require.Equal(t, "1000000", formatFloat(1e6))
}

// --- detectParseConfig ---

func TestDetectParseConfig(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  parseMode
	}{
		{"no pipeline", `{stack="observability"}`, parseModeNone},
		{"json at end", `{stack="observability"} |= "" | json`, parseModeJSON},
		{"json with space after", `{stack="observability"} | json | line_format "{{.msg}}"`, parseModeJSON},
		{"json followed by pipe", `{stack="observability"} | json|label_format`, parseModeJSON},
		{"logfmt", `{stack="observability"} | logfmt`, parseModeLogfmt},
		{"logfmt with trailing space", `{stack="observability"} | logfmt `, parseModeLogfmt},
		{"no match json_extract", `{stack="observability"} | json_extract`, parseModeNone},
		{"case insensitive JSON", `{stack="observability"} | JSON`, parseModeJSON},
		{"empty query", "", parseModeNone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectParseConfig(tt.query)
			require.Equal(t, tt.want, got.mode)
		})
	}
}

func TestDetectParseConfig_JSONExpressions(t *testing.T) {
	tests := []struct {
		name      string
		query     string
		wantExprs []jsonFieldExpr
	}{
		{
			"no expressions",
			`{app="test"} | json`,
			nil,
		},
		{
			"single field",
			`{app="test"} | json level`,
			[]jsonFieldExpr{{label: "level", path: ""}},
		},
		{
			"multiple fields",
			`{app="test"} | json level, caller, msg`,
			[]jsonFieldExpr{
				{label: "level", path: ""},
				{label: "caller", path: ""},
				{label: "msg", path: ""},
			},
		},
		{
			"field with path",
			`{app="test"} | json sev="level"`,
			[]jsonFieldExpr{{label: "sev", path: "level"}},
		},
		{
			"nested path",
			`{app="test"} | json ip="net.src", level`,
			[]jsonFieldExpr{
				{label: "ip", path: "net.src"},
				{label: "level", path: ""},
			},
		},
		{
			"expressions followed by pipe",
			`{app="test"} | json level, caller | line_format "{{.level}}"`,
			[]jsonFieldExpr{
				{label: "level", path: ""},
				{label: "caller", path: ""},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectParseConfig(tt.query)
			require.Equal(t, parseModeJSON, got.mode)
			require.Equal(t, tt.wantExprs, got.jsonExprs)
		})
	}
}

// --- extractJSONFields ---

func TestExtractJSONFields(t *testing.T) {
	labels := map[string]string{"hostname": "web1"}
	msg := `{"level":"info","caller":"handler.go:433","msg":"query stats","status_code":200,"latency":0.003,"active":true}`

	extractJSONFields(msg, labels, nil)

	require.Equal(t, "web1", labels["hostname"]) // Original label preserved.
	require.Equal(t, "info", labels["level"])
	require.Equal(t, "handler.go:433", labels["caller"])
	require.Equal(t, "query stats", labels["msg"])
	require.Equal(t, "200", labels["status_code"])
	require.Equal(t, "0.003", labels["latency"])
	require.Equal(t, "true", labels["active"])
}

func TestExtractJSONFields_NoOverwrite(t *testing.T) {
	labels := map[string]string{"level": "original"}
	msg := `{"level":"info","caller":"handler.go:433"}`

	extractJSONFields(msg, labels, nil)

	// Existing stream label takes precedence.
	require.Equal(t, "original", labels["level"])
	// New field is still extracted.
	require.Equal(t, "handler.go:433", labels["caller"])
}

func TestExtractJSONFields_InvalidJSON(t *testing.T) {
	labels := map[string]string{"hostname": "web1"}
	extractJSONFields("not json at all", labels, nil)
	// No fields added; original preserved.
	require.Len(t, labels, 1)
	require.Equal(t, "web1", labels["hostname"])
}

func TestExtractJSONFields_NestedObjects(t *testing.T) {
	labels := map[string]string{}
	msg := `{"level":"info","details":{"key":"val"},"tags":["a","b"],"nothing":null}`

	extractJSONFields(msg, labels, nil)

	require.Equal(t, "info", labels["level"])
	require.Equal(t, `{"key":"val"}`, labels["details"])
	require.Equal(t, `["a","b"]`, labels["tags"])
	require.Equal(t, "", labels["nothing"]) // null -> empty string
}

func TestExtractJSONFields_SelectiveFields(t *testing.T) {
	labels := map[string]string{}
	msg := `{"level":"info","caller":"handler.go:433","msg":"query stats","status_code":200}`

	exprs := []jsonFieldExpr{
		{label: "level", path: ""},
		{label: "msg", path: ""},
	}
	extractJSONFields(msg, labels, exprs)

	require.Equal(t, "info", labels["level"])
	require.Equal(t, "query stats", labels["msg"])
	// caller and status_code should NOT be extracted.
	require.Len(t, labels, 2)
}

func TestExtractJSONFields_PathExpression(t *testing.T) {
	labels := map[string]string{}
	msg := `{"net":{"src":"10.0.0.1","dst":"10.0.0.2"},"level":"info"}`

	exprs := []jsonFieldExpr{
		{label: "src_ip", path: "net.src"},
		{label: "level", path: ""},
	}
	extractJSONFields(msg, labels, exprs)

	require.Equal(t, "10.0.0.1", labels["src_ip"])
	require.Equal(t, "info", labels["level"])
	require.Len(t, labels, 2)
}

func TestExtractJSONFields_PathExpression_DeepNesting(t *testing.T) {
	labels := map[string]string{}
	msg := `{"a":{"b":{"c":"deep_value"}}}`

	exprs := []jsonFieldExpr{
		{label: "val", path: "a.b.c"},
	}
	extractJSONFields(msg, labels, exprs)

	require.Equal(t, "deep_value", labels["val"])
}

func TestExtractJSONFields_PathExpression_Missing(t *testing.T) {
	labels := map[string]string{}
	msg := `{"level":"info"}`

	exprs := []jsonFieldExpr{
		{label: "missing", path: "does.not.exist"},
		{label: "level", path: ""},
	}
	extractJSONFields(msg, labels, exprs)

	require.Equal(t, "info", labels["level"])
	// Missing path should not produce a label.
	_, exists := labels["missing"]
	require.False(t, exists)
}

// --- anyToString ---

func TestAnyToString(t *testing.T) {
	require.Equal(t, "hello", anyToString("hello"))
	require.Equal(t, "42", anyToString(float64(42)))
	require.Equal(t, "3.14", anyToString(float64(3.14)))
	require.Equal(t, "1000000", anyToString(float64(1e6)))
	require.Equal(t, "true", anyToString(true))
	require.Equal(t, "false", anyToString(false))
	require.Equal(t, "", anyToString(nil))
}

// --- Full | json flow through TransformResponse ---

func TestTransformResponse_LogQuery_JSONExtraction(t *testing.T) {
	body := strings.Join([]string{
		`{"_time":"2024-01-01T00:00:00Z","_msg":"{\"level\":\"info\",\"caller\":\"handler.go:433\",\"msg\":\"query stats\"}","hostname":"web1"}`,
		`{"_time":"2024-01-01T00:00:01Z","_msg":"{\"level\":\"error\",\"caller\":\"handler.go:500\",\"msg\":\"failed\"}","hostname":"web1"}`,
	}, "\n")

	resp, err := TransformResponse(
		LokiPathQueryRange, logsql.QueryKindLogs,
		makeVLogsResponse(body),
		[]string{"hostname"},
		url.Values{"query": []string{`{hostname="web1"} |= "" | json`}},
	)
	require.NoError(t, err)

	result := readJSON(t, resp)
	data := result["data"].(map[string]any)
	require.Equal(t, "streams", data["resultType"])

	streams := data["result"].([]any)
	// Two lines with different "level" values -> two streams.
	require.Len(t, streams, 2)

	// Find the info stream and error stream.
	var infoStream, errorStream map[string]any
	for _, s := range streams {
		sm := s.(map[string]any)
		labels := sm["stream"].(map[string]any)
		if labels["level"] == "info" {
			infoStream = sm
		} else if labels["level"] == "error" {
			errorStream = sm
		}
	}
	require.NotNil(t, infoStream, "should have info stream")
	require.NotNil(t, errorStream, "should have error stream")

	// Verify extracted fields in info stream.
	infoLabels := infoStream["stream"].(map[string]any)
	require.Equal(t, "web1", infoLabels["hostname"])
	require.Equal(t, "info", infoLabels["level"])
	require.Equal(t, "handler.go:433", infoLabels["caller"])
	require.Equal(t, "query stats", infoLabels["msg"])

	// Verify extracted fields in error stream.
	errorLabels := errorStream["stream"].(map[string]any)
	require.Equal(t, "web1", errorLabels["hostname"])
	require.Equal(t, "error", errorLabels["level"])
	require.Equal(t, "failed", errorLabels["msg"])
}

func TestTransformResponse_LogQuery_NoJSONWithoutPipeline(t *testing.T) {
	// Without | json in the query, _msg should NOT be parsed.
	body := `{"_time":"2024-01-01T00:00:00Z","_msg":"{\"level\":\"info\",\"caller\":\"handler.go:433\"}","hostname":"web1"}`

	resp, err := TransformResponse(
		LokiPathQueryRange, logsql.QueryKindLogs,
		makeVLogsResponse(body),
		[]string{"hostname"},
		url.Values{"query": []string{`{hostname="web1"}`}},
	)
	require.NoError(t, err)

	result := readJSON(t, resp)
	data := result["data"].(map[string]any)
	streams := data["result"].([]any)
	require.Len(t, streams, 1)

	labels := streams[0].(map[string]any)["stream"].(map[string]any)
	// Only hostname should be present -- no extracted fields.
	require.Equal(t, "web1", labels["hostname"])
	require.Nil(t, labels["level"], "level should not be extracted without | json")
	require.Nil(t, labels["caller"], "caller should not be extracted without | json")
}

func TestTransformResponse_LogQuery_JSONWithNonJSONMsg(t *testing.T) {
	// | json requested but _msg is not valid JSON -- should still work,
	// just no extra fields extracted.
	body := `{"_time":"2024-01-01T00:00:00Z","_msg":"plain text log line","hostname":"web1"}`

	resp, err := TransformResponse(
		LokiPathQueryRange, logsql.QueryKindLogs,
		makeVLogsResponse(body),
		[]string{"hostname"},
		url.Values{"query": []string{`{hostname="web1"} | json`}},
	)
	require.NoError(t, err)

	result := readJSON(t, resp)
	data := result["data"].(map[string]any)
	streams := data["result"].([]any)
	require.Len(t, streams, 1)

	labels := streams[0].(map[string]any)["stream"].(map[string]any)
	require.Equal(t, "web1", labels["hostname"])
	// No extra fields -- _msg was not valid JSON.
	require.Len(t, labels, 1)
}

// --- Full | json with selective extraction through TransformResponse ---

func TestTransformResponse_LogQuery_JSONSelectiveExtraction(t *testing.T) {
	body := strings.Join([]string{
		`{"_time":"2024-01-01T00:00:00Z","_msg":"{\"level\":\"info\",\"caller\":\"handler.go:433\",\"msg\":\"query stats\",\"status_code\":200}","hostname":"web1"}`,
	}, "\n")

	resp, err := TransformResponse(
		LokiPathQueryRange, logsql.QueryKindLogs,
		makeVLogsResponse(body),
		[]string{"hostname"},
		url.Values{"query": []string{`{hostname="web1"} | json level, caller`}},
	)
	require.NoError(t, err)

	result := readJSON(t, resp)
	data := result["data"].(map[string]any)
	streams := data["result"].([]any)
	require.Len(t, streams, 1)

	labels := streams[0].(map[string]any)["stream"].(map[string]any)
	require.Equal(t, "web1", labels["hostname"])
	require.Equal(t, "info", labels["level"])
	require.Equal(t, "handler.go:433", labels["caller"])
	// msg and status_code should NOT be extracted (not in expression list).
	require.Nil(t, labels["msg"])
	require.Nil(t, labels["status_code"])
}

// --- Full | json with path expression through TransformResponse ---

func TestTransformResponse_LogQuery_JSONPathExtraction(t *testing.T) {
	body := strings.Join([]string{
		`{"_time":"2024-01-01T00:00:00Z","_msg":"{\"net\":{\"src\":\"10.0.0.1\",\"dst\":\"10.0.0.2\"},\"level\":\"info\"}","hostname":"web1"}`,
	}, "\n")

	resp, err := TransformResponse(
		LokiPathQueryRange, logsql.QueryKindLogs,
		makeVLogsResponse(body),
		[]string{"hostname"},
		url.Values{"query": []string{`{hostname="web1"} | json ip="net.src", level`}},
	)
	require.NoError(t, err)

	result := readJSON(t, resp)
	data := result["data"].(map[string]any)
	streams := data["result"].([]any)
	require.Len(t, streams, 1)

	labels := streams[0].(map[string]any)["stream"].(map[string]any)
	require.Equal(t, "web1", labels["hostname"])
	require.Equal(t, "10.0.0.1", labels["ip"])
	require.Equal(t, "info", labels["level"])
	// net and dst should NOT be extracted.
	require.Nil(t, labels["net"])
	require.Nil(t, labels["dst"])
}

// --- Full | logfmt flow through TransformResponse ---

func TestTransformResponse_LogQuery_LogfmtExtraction(t *testing.T) {
	body := strings.Join([]string{
		`{"_time":"2024-01-01T00:00:00Z","_msg":"level=info caller=handler.go:433 msg=\"query stats\"","hostname":"web1"}`,
		`{"_time":"2024-01-01T00:00:01Z","_msg":"level=error caller=handler.go:500 msg=failed","hostname":"web1"}`,
	}, "\n")

	resp, err := TransformResponse(
		LokiPathQueryRange, logsql.QueryKindLogs,
		makeVLogsResponse(body),
		[]string{"hostname"},
		url.Values{"query": []string{`{hostname="web1"} | logfmt`}},
	)
	require.NoError(t, err)

	result := readJSON(t, resp)
	data := result["data"].(map[string]any)
	require.Equal(t, "streams", data["resultType"])

	streams := data["result"].([]any)
	// Two lines with different level -> two streams.
	require.Len(t, streams, 2)

	var infoStream, errorStream map[string]any
	for _, s := range streams {
		sm := s.(map[string]any)
		labels := sm["stream"].(map[string]any)
		if labels["level"] == "info" {
			infoStream = sm
		} else if labels["level"] == "error" {
			errorStream = sm
		}
	}
	require.NotNil(t, infoStream, "should have info stream")
	require.NotNil(t, errorStream, "should have error stream")

	infoLabels := infoStream["stream"].(map[string]any)
	require.Equal(t, "web1", infoLabels["hostname"])
	require.Equal(t, "info", infoLabels["level"])
	require.Equal(t, "handler.go:433", infoLabels["caller"])
	require.Equal(t, "query stats", infoLabels["msg"])

	errorLabels := errorStream["stream"].(map[string]any)
	require.Equal(t, "web1", errorLabels["hostname"])
	require.Equal(t, "error", errorLabels["level"])
	require.Equal(t, "failed", errorLabels["msg"])
}

func TestTransformResponse_LogQuery_LogfmtWithNonLogfmtMsg(t *testing.T) {
	// | logfmt requested but _msg has no key=value pairs. Best-effort
	// extraction should still work (the whole line is treated as a bare key).
	body := `{"_time":"2024-01-01T00:00:00Z","_msg":"plain text log line","hostname":"web1"}`

	resp, err := TransformResponse(
		LokiPathQueryRange, logsql.QueryKindLogs,
		makeVLogsResponse(body),
		[]string{"hostname"},
		url.Values{"query": []string{`{hostname="web1"} | logfmt`}},
	)
	require.NoError(t, err)

	result := readJSON(t, resp)
	data := result["data"].(map[string]any)
	streams := data["result"].([]any)
	require.Len(t, streams, 1)
	// Should have hostname + best-effort parsed logfmt keys.
	labels := streams[0].(map[string]any)["stream"].(map[string]any)
	require.Equal(t, "web1", labels["hostname"])
}

// --- extractLogfmtFields ---

func TestExtractLogfmtFields_Basic(t *testing.T) {
	labels := map[string]string{}
	extractLogfmtFields("level=info caller=handler.go:433 status=200", labels)

	require.Equal(t, "info", labels["level"])
	require.Equal(t, "handler.go:433", labels["caller"])
	require.Equal(t, "200", labels["status"])
}

func TestExtractLogfmtFields_QuotedValues(t *testing.T) {
	labels := map[string]string{}
	extractLogfmtFields(`msg="query stats" path="/api/v1/query"`, labels)

	require.Equal(t, "query stats", labels["msg"])
	require.Equal(t, "/api/v1/query", labels["path"])
}

func TestExtractLogfmtFields_EscapedQuotes(t *testing.T) {
	labels := map[string]string{}
	extractLogfmtFields(`msg="say \"hello\""`, labels)

	require.Equal(t, `say "hello"`, labels["msg"])
}

func TestExtractLogfmtFields_EmptyValue(t *testing.T) {
	labels := map[string]string{}
	extractLogfmtFields("level= caller=handler.go:433", labels)

	require.Equal(t, "", labels["level"])
	require.Equal(t, "handler.go:433", labels["caller"])
}

func TestExtractLogfmtFields_BareKeys(t *testing.T) {
	labels := map[string]string{}
	extractLogfmtFields("debug level=info", labels)

	// "debug" has no = -> bare key with empty value.
	require.Equal(t, "", labels["debug"])
	require.Equal(t, "info", labels["level"])
}

func TestExtractLogfmtFields_NoOverwrite(t *testing.T) {
	labels := map[string]string{"level": "original"}
	extractLogfmtFields("level=info caller=handler.go:433", labels)

	require.Equal(t, "original", labels["level"])
	require.Equal(t, "handler.go:433", labels["caller"])
}

func TestExtractLogfmtFields_EmptyMsg(t *testing.T) {
	labels := map[string]string{"hostname": "web1"}
	extractLogfmtFields("", labels)

	require.Len(t, labels, 1)
	require.Equal(t, "web1", labels["hostname"])
}

// --- navigateJSONPath ---

func TestNavigateJSONPath_TopLevel(t *testing.T) {
	obj := map[string]any{"level": "info"}
	v, ok := navigateJSONPath(obj, "level")
	require.True(t, ok)
	require.Equal(t, "info", v)
}

func TestNavigateJSONPath_Nested(t *testing.T) {
	obj := map[string]any{
		"net": map[string]any{
			"src": "10.0.0.1",
			"dst": "10.0.0.2",
		},
	}
	v, ok := navigateJSONPath(obj, "net.src")
	require.True(t, ok)
	require.Equal(t, "10.0.0.1", v)
}

func TestNavigateJSONPath_DeepNesting(t *testing.T) {
	obj := map[string]any{
		"a": map[string]any{
			"b": map[string]any{
				"c": map[string]any{
					"d": "deep",
				},
			},
		},
	}
	v, ok := navigateJSONPath(obj, "a.b.c.d")
	require.True(t, ok)
	require.Equal(t, "deep", v)
}

func TestNavigateJSONPath_Missing(t *testing.T) {
	obj := map[string]any{"level": "info"}
	_, ok := navigateJSONPath(obj, "does.not.exist")
	require.False(t, ok)
}

func TestNavigateJSONPath_MissingIntermediate(t *testing.T) {
	obj := map[string]any{"net": "not-an-object"}
	_, ok := navigateJSONPath(obj, "net.src")
	require.False(t, ok)
}

func TestNavigateJSONPath_EmptyPath(t *testing.T) {
	// Single-segment empty path returns the root (edge case).
	obj := map[string]any{"": "empty-key"}
	v, ok := navigateJSONPath(obj, "")
	require.True(t, ok)
	require.Equal(t, "empty-key", v)
}

// --- parseJSONExprs ---

func TestParseJSONExprs_Empty(t *testing.T) {
	require.Nil(t, parseJSONExprs(""))
	require.Nil(t, parseJSONExprs("   "))
}

func TestParseJSONExprs_SingleField(t *testing.T) {
	exprs := parseJSONExprs("level")
	require.Equal(t, []jsonFieldExpr{{label: "level", path: ""}}, exprs)
}

func TestParseJSONExprs_MultipleFields(t *testing.T) {
	exprs := parseJSONExprs("level, caller, msg")
	require.Equal(t, []jsonFieldExpr{
		{label: "level", path: ""},
		{label: "caller", path: ""},
		{label: "msg", path: ""},
	}, exprs)
}

func TestParseJSONExprs_PathExpression(t *testing.T) {
	exprs := parseJSONExprs(`sev="level"`)
	require.Equal(t, []jsonFieldExpr{{label: "sev", path: "level"}}, exprs)
}

func TestParseJSONExprs_NestedPath(t *testing.T) {
	exprs := parseJSONExprs(`ip="net.src"`)
	require.Equal(t, []jsonFieldExpr{{label: "ip", path: "net.src"}}, exprs)
}

func TestParseJSONExprs_MixedFieldsAndPaths(t *testing.T) {
	exprs := parseJSONExprs(`ip="net.src", level, sev="severity"`)
	require.Equal(t, []jsonFieldExpr{
		{label: "ip", path: "net.src"},
		{label: "level", path: ""},
		{label: "sev", path: "severity"},
	}, exprs)
}

// readJSON reads the response body and unmarshals into a generic map.
func readJSON(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var result map[string]any
	require.NoError(t, json.Unmarshal(body, &result))
	return result
}
