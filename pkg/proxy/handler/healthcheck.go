package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/grafana/loki/v3/pkg/logqlmodel/stats"
)

// grafanaHealthCheckQuery is the exact LogQL expression Grafana sends when
// testing a Loki datasource connection (see pkg/tsdb/loki/healthcheck.go in
// the Grafana source).  No real query would use this expression, so matching
// on it is safe.
const grafanaHealthCheckQuery = "vector(1)+vector(1)"

// IsGrafanaHealthCheck reports whether r looks like the Grafana datasource
// health check request.  Detection is based on the query parameter; Grafana
// does not send any health-check-specific header (the internal refID
// "__healthcheck__" stays inside the plugin SDK).
func IsGrafanaHealthCheck(r *http.Request) bool {
	return r.URL.Query().Get("query") == grafanaHealthCheckQuery
}

// WriteGrafanaHealthCheckResponse writes a static Loki-compatible vector
// response that satisfies the Grafana health check expectations:
//
//   - Exactly 1 vector result
//   - metric: {} (empty)
//   - value: [<time>, "2"]
//
// The time parameter is taken from the request query string.  Grafana sends
// time values in nanoseconds (see pkg/tsdb/loki/api.go: strconv.FormatInt(
// query.End.UnixNano(), 10)).  For the health check the sentinel value is
// 4000000000 (i.e. time.Unix(4,0).UnixNano()).  We convert nanoseconds to
// seconds because Loki returns Prometheus-style timestamps (seconds as a
// JSON number).
func WriteGrafanaHealthCheckResponse(w http.ResponseWriter, r *http.Request, logger log.Logger) {
	// Default: current time in seconds (Prometheus convention).
	timestamp := float64(time.Now().Unix())
	if t := r.URL.Query().Get("time"); t != "" {
		if parsed, err := strconv.ParseInt(t, 10, 64); err == nil {
			// Grafana always sends nanoseconds; convert to seconds.
			timestamp = float64(parsed) / 1e9
		}
	}

	response := map[string]any{
		"status": "success",
		"data": map[string]any{
			"resultType": "vector",
			"result": []map[string]any{
				{
					"metric": map[string]string{},
					"value":  []any{timestamp, "2"},
				},
			},
			"stats": stats.Result{},
		},
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		level.Error(logger).Log("msg", "Failed to encode health check response", "err", err)
	}
}
