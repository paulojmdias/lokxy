package victorialogs

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/VictoriaMetrics-Community/logql-to-logsql/lib/logsql"
)

// RewriteRequest builds a new *http.Request targeting a VictoriaLogs
// backend. It translates the Loki API path to the appropriate
// VictoriaLogs endpoint and maps query parameters.
//
// The request is sent as POST with application/x-www-form-urlencoded
// body, matching VictoriaLogs API convention.
//
// Parameters:
//   - ctx: request context (carries tracing, cancellation)
//   - orig: the original Loki-style HTTP request
//   - qi: translated query info (LogsQL string + kind)
//   - baseURL: VictoriaLogs instance base URL (e.g., "https://vlogs.example.com")
//   - tenant: optional tenant prefix (e.g., "0:0"), empty string if unused
//   - bodyBytes: the original request body (for extracting POST form params)
func RewriteRequest(
	ctx context.Context,
	orig *http.Request,
	qi *logsql.QueryInfo,
	baseURL string,
	tenant string,
	bodyBytes []byte,
) (*http.Request, error) {
	// Resolve the VictoriaLogs endpoint path.
	vlogsPath, ok := MapEndpoint(orig.URL.Path, qi.Kind)
	if !ok {
		return nil, fmt.Errorf("no VictoriaLogs endpoint mapping for %s", orig.URL.Path)
	}

	// Apply tenant prefix.
	fullPath := BuildVLogsPath(vlogsPath, tenant)

	// Build the target URL.
	targetURL, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid VictoriaLogs base URL %q: %w", baseURL, err)
	}
	targetURL.Path = strings.TrimRight(targetURL.Path, "/") + fullPath

	// Build form values from the original request parameters and the
	// translated query.
	form := buildFormValues(orig, qi, bodyBytes)

	// Create POST request with form-encoded body.
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		targetURL.String(),
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create VictoriaLogs request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	return req, nil
}

// buildFormValues constructs the VictoriaLogs form parameters from the
// original Loki request and translated query info.
func buildFormValues(orig *http.Request, qi *logsql.QueryInfo, bodyBytes []byte) url.Values {
	// Collect all original parameters (GET + POST form).
	origParams := CollectOrigParams(orig, bodyBytes)
	form := url.Values{}

	// Always set the translated query.
	form.Set("query", qi.LogsQL)

	switch {
	case isLabelValuesPath(orig.URL.Path):
		buildLabelValuesParams(form, orig.URL.Path, origParams)
	case orig.URL.Path == LokiPathLabels:
		buildLabelsParams(form, origParams)
	case orig.URL.Path == LokiPathSeries:
		buildSeriesParams(form, origParams)
	case qi.Kind == logsql.QueryKindStats:
		buildStatsParams(form, orig.URL.Path, origParams)
	default:
		// Log queries.
		buildLogQueryParams(form, origParams)
	}

	return form
}

// buildLogQueryParams sets parameters for /select/logsql/query.
func buildLogQueryParams(form, origParams url.Values) {
	if v := origParams.Get("start"); v != "" {
		form.Set("start", v)
	}
	if v := origParams.Get("end"); v != "" {
		form.Set("end", v)
	}
	if v := origParams.Get("limit"); v != "" {
		form.Set("limit", v)
	}
	if v := origParams.Get("direction"); v != "" {
		form.Set("direction", v)
	}
}

// buildStatsParams sets parameters for stats_query or stats_query_range.
func buildStatsParams(form url.Values, lokiPath string, origParams url.Values) {
	if v := origParams.Get("start"); v != "" {
		form.Set("start", v)
	}
	if v := origParams.Get("end"); v != "" {
		form.Set("end", v)
	}
	// step is only relevant for range queries.
	if lokiPath == LokiPathQueryRange {
		if v := origParams.Get("step"); v != "" {
			form.Set("step", v)
		}
	}
	// For instant stats queries, VictoriaLogs uses "time" instead of
	// "end" to specify the evaluation timestamp.
	if lokiPath == LokiPathQuery || lokiPath == LokiPathIndexStats {
		if v := origParams.Get("time"); v != "" {
			form.Set("time", v)
		} else if v := origParams.Get("end"); v != "" {
			// Loki instant queries use the "time" param, but Grafana
			// sometimes sends "end" instead. Forward as "time".
			form.Set("time", v)
		}
	}
}

// buildLabelsParams sets parameters for /select/logsql/field_names.
func buildLabelsParams(form, origParams url.Values) {
	if v := origParams.Get("start"); v != "" {
		form.Set("start", v)
	}
	if v := origParams.Get("end"); v != "" {
		form.Set("end", v)
	}
}

// buildLabelValuesParams sets parameters for /select/logsql/field_values.
func buildLabelValuesParams(form url.Values, lokiPath string, origParams url.Values) {
	// Extract the field name from the Loki path.
	fieldName := ExtractLabelName(lokiPath)
	if fieldName != "" {
		form.Set("field", fieldName)
	}
	if v := origParams.Get("start"); v != "" {
		form.Set("start", v)
	}
	if v := origParams.Get("end"); v != "" {
		form.Set("end", v)
	}
}

// buildSeriesParams sets parameters for /select/logsql/streams.
func buildSeriesParams(form, origParams url.Values) {
	if v := origParams.Get("start"); v != "" {
		form.Set("start", v)
	}
	if v := origParams.Get("end"); v != "" {
		form.Set("end", v)
	}
}

// CollectOrigParams merges GET query params and POST form body params
// into a single url.Values. GET params take precedence. Exported for
// use in response transformation (needs eval time from original request).
func CollectOrigParams(r *http.Request, bodyBytes []byte) url.Values {
	params := make(url.Values)

	// Start with POST form body if present.
	if len(bodyBytes) > 0 {
		if formValues, err := url.ParseQuery(string(bodyBytes)); err == nil {
			for k, vs := range formValues {
				for _, v := range vs {
					params.Add(k, v)
				}
			}
		}
	}

	// GET query params override POST body values.
	for k, vs := range r.URL.Query() {
		params.Del(k) // Remove any POST body value for this key.
		for _, v := range vs {
			params.Add(k, v)
		}
	}

	return params
}
