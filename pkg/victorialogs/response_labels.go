package victorialogs

import (
	"encoding/json"
	"fmt"
	"sort"
)

// vlogsValuesResponse is the common response format used by VictoriaLogs
// for field_names, field_values, and streams endpoints.
//
//	{"values":[{"value":"field_or_stream","hits":N},...]}
type vlogsValuesResponse struct {
	Values []vlogsValueEntry `json:"values"`
}

type vlogsValueEntry struct {
	Value string `json:"value"`
	Hits  int64  `json:"hits"`
}

// transformFieldNames converts a VictoriaLogs field_names response
// into a Loki labels response.
//
// VictoriaLogs returns:
//
//	{"values":[{"value":"hostname","hits":1000},{"value":"app","hits":500}]}
//
// Loki expects:
//
//	{"status":"success","data":["app","hostname"]}
func transformFieldNames(body []byte) ([]byte, error) {
	if len(body) == 0 {
		return wrapDataResponse([]string{})
	}

	var resp vlogsValuesResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse VictoriaLogs field_names response: %w", err)
	}

	names := make([]string, 0, len(resp.Values))
	for _, entry := range resp.Values {
		if entry.Value != "" {
			names = append(names, entry.Value)
		}
	}

	sort.Strings(names)
	return wrapDataResponse(names)
}

// transformFieldValues converts a VictoriaLogs field_values response
// into a Loki label values response.
//
// VictoriaLogs returns:
//
//	{"values":[{"value":"web1","hits":500},{"value":"web2","hits":300}]}
//
// Loki expects:
//
//	{"status":"success","data":["web1","web2"]}
func transformFieldValues(body []byte) ([]byte, error) {
	if len(body) == 0 {
		return wrapDataResponse([]string{})
	}

	var resp vlogsValuesResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse VictoriaLogs field_values response: %w", err)
	}

	values := make([]string, 0, len(resp.Values))
	for _, entry := range resp.Values {
		values = append(values, entry.Value)
	}

	sort.Strings(values)
	return wrapDataResponse(values)
}
