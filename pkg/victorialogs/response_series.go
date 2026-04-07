package victorialogs

import (
	"encoding/json"
	"fmt"
)

// transformSeries converts a VictoriaLogs streams response into
// a Loki series response.
//
// VictoriaLogs returns:
//
//	{"values":[
//	  {"value":"{hostname=\"web1\",app=\"nginx\"}","hits":1000},
//	  {"value":"{hostname=\"web2\",app=\"nginx\"}","hits":500}
//	]}
//
// Loki expects:
//
//	{"status":"success","data":[
//	  {"hostname":"web1","app":"nginx"},
//	  {"hostname":"web2","app":"nginx"}
//	]}
func transformSeries(body []byte) ([]byte, error) {
	if len(body) == 0 {
		return wrapDataResponse([]map[string]string{})
	}

	var resp vlogsValuesResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse VictoriaLogs streams response: %w", err)
	}

	series := make([]map[string]string, 0, len(resp.Values))
	for _, entry := range resp.Values {
		labels, err := parsePromStyleLabels(entry.Value)
		if err != nil {
			// Skip malformed stream entries.
			continue
		}
		series = append(series, labels)
	}

	return wrapDataResponse(series)
}
