package victorialogs

import (
	"encoding/json"
	"strconv"
)

// transformStatsInstant passes through a VictoriaLogs stats_query
// response which is already in Prometheus/Loki vector format.
//
// VictoriaLogs returns:
//
//	{"status":"success","data":{"resultType":"vector","result":[
//	  {"metric":{"__name__":"total"},"value":[1704067200.000,"42"]}
//	]}}
//
// Loki expects the same format (with an optional empty "stats" field).
// We add the "stats" field if missing to match Loki handler expectations.
func transformStatsInstant(body []byte) ([]byte, error) {
	return ensureStatsField(body)
}

// transformStatsRange passes through a VictoriaLogs stats_query_range
// response which is already in Prometheus/Loki matrix format.
//
// VictoriaLogs returns:
//
//	{"status":"success","data":{"resultType":"matrix","result":[
//	  {"metric":{"__name__":"total"},"values":[[1704067200,"10"],[1704070800,"15"]]}
//	]}}
//
// Loki expects the same format (with an optional empty "stats" field).
func transformStatsRange(body []byte) ([]byte, error) {
	return ensureStatsField(body)
}

// ensureStatsField adds an empty "stats" field to the "data" object
// if it is missing, to match the shape Loki merge handlers expect.
func ensureStatsField(body []byte) ([]byte, error) {
	var resp map[string]any
	if err := json.Unmarshal(body, &resp); err != nil {
		// If we can't parse it, pass through as-is.
		return body, nil
	}

	if data, ok := resp["data"].(map[string]any); ok {
		if _, hasStats := data["stats"]; !hasStats {
			data["stats"] = map[string]any{}
		}
	}

	return json.Marshal(resp)
}

// transformIndexStats converts a VictoriaLogs stats_query (Prometheus
// vector) response into Loki's index/stats format.
//
// The proxy sends a query like:
//
//	<logsql> | stats count() as entries, count_uniq(_stream) as streams, sum_len(_msg) as bytes
//
// to the VictoriaLogs stats_query endpoint. The response is a standard
// Prometheus vector with one entry per stat alias, identified by __name__:
//
//	{"status":"success","data":{"resultType":"vector","result":[
//	  {"metric":{"__name__":"entries"},"value":[1704067200.000,"5000"]},
//	  {"metric":{"__name__":"streams"},"value":[1704067200.000,"42"]},
//	  {"metric":{"__name__":"bytes"},"value":[1704067200.000,"1234567"]}
//	]}}
//
// This function extracts the named values and maps them to the Loki format:
//
//	{"streams":42,"chunks":0,"bytes":1234567,"entries":5000}
//
// "chunks" remains 0 because VictoriaLogs has no direct equivalent
// for this Loki-internal concept.
func transformIndexStats(body []byte) ([]byte, error) {
	vals := make(map[string]int64)

	var resp map[string]any
	if err := json.Unmarshal(body, &resp); err == nil {
		vals = extractNamedVectorValues(resp)
	}
	// If parsing fails, vals stays empty -> all zeros (graceful degradation).

	result := map[string]int64{
		"streams": vals["streams"],
		"chunks":  0, // no VictoriaLogs equivalent
		"bytes":   vals["bytes"],
		"entries": vals["entries"],
	}
	return json.Marshal(result)
}

// extractNamedVectorValues extracts named metric values from a
// Prometheus vector response where each result entry has a __name__
// metric label identifying the stat.
//
// Expected structure (from VictoriaLogs multi-stats pipe):
//
//	{"data":{"result":[
//	  {"metric":{"__name__":"entries"},"value":[<ts>,"5000"]},
//	  {"metric":{"__name__":"streams"},"value":[<ts>,"42"]},
//	  {"metric":{"__name__":"bytes"},"value":[<ts>,"1234567"]}
//	]}}
func extractNamedVectorValues(resp map[string]any) map[string]int64 {
	vals := make(map[string]int64)

	data, ok := resp["data"].(map[string]any)
	if !ok {
		return vals
	}
	results, ok := data["result"].([]any)
	if !ok {
		return vals
	}

	for _, r := range results {
		entry, ok := r.(map[string]any)
		if !ok {
			continue
		}

		// Extract the stat name from metric.__name__.
		metric, ok := entry["metric"].(map[string]any)
		if !ok {
			continue
		}
		name, ok := metric["__name__"].(string)
		if !ok || name == "" {
			continue
		}

		// Extract the numeric value from value[1].
		value, ok := entry["value"].([]any)
		if !ok || len(value) < 2 {
			continue
		}

		vals[name] = parseVectorValue(value[1])
	}

	return vals
}

// parseVectorValue parses a Prometheus vector value element into an int64.
// The value is typically a string like "5000" or "5000.0", but may also
// be a JSON number (float64).
func parseVectorValue(v any) int64 {
	switch val := v.(type) {
	case string:
		n, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			// Try float (e.g., "5000.0").
			f, err := strconv.ParseFloat(val, 64)
			if err != nil {
				return 0
			}
			return int64(f)
		}
		return n
	case float64:
		return int64(val)
	default:
		return 0
	}
}
