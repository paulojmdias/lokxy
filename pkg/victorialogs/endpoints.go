package victorialogs

import (
	"github.com/VictoriaMetrics-Community/logql-to-logsql/lib/logsql"
)

// VictoriaLogs API paths.
const (
	VLogsPathQuery           = "/select/logsql/query"
	VLogsPathStatsQuery      = "/select/logsql/stats_query"
	VLogsPathStatsQueryRange = "/select/logsql/stats_query_range"
	VLogsPathFieldNames      = "/select/logsql/field_names"
	VLogsPathFieldValues     = "/select/logsql/field_values"
	VLogsPathStreams         = "/select/logsql/streams"
)

// Loki API paths.
const (
	LokiPathQuery            = "/loki/api/v1/query"
	LokiPathQueryRange       = "/loki/api/v1/query_range"
	LokiPathLabels           = "/loki/api/v1/labels"
	LokiPathSeries           = "/loki/api/v1/series"
	LokiPathIndexStats       = "/loki/api/v1/index/stats"
	LokiPathTail             = "/loki/api/v1/tail"
	LokiPathPatterns         = "/loki/api/v1/patterns"
	LokiPathDetectedLabels   = "/loki/api/v1/detected_labels"
	LokiPathDetectedFields   = "/loki/api/v1/detected_fields"
	LokiPathIndexVolume      = "/loki/api/v1/index/volume"
	LokiPathIndexVolumeRange = "/loki/api/v1/index/volume_range"
)

// unsupportedEndpoints lists Loki paths with no VictoriaLogs equivalent.
// VictoriaLogs backends silently skip these so Loki backends in mixed
// mode can still handle them.
var unsupportedEndpoints = map[string]bool{
	LokiPathTail:             true,
	LokiPathPatterns:         true,
	LokiPathDetectedLabels:   true,
	LokiPathDetectedFields:   true,
	LokiPathIndexVolume:      true,
	LokiPathIndexVolumeRange: true,
	// /loki/api/v1/detected_field/{name}/values is handled by prefix
	// check in IsSupportedEndpoint.
}

// IsSupportedEndpoint returns true if the given Loki API path has a
// VictoriaLogs equivalent. Unsupported paths cause the VictoriaLogs
// backend to be silently skipped (return nil, not error).
func IsSupportedEndpoint(lokiPath string) bool {
	if unsupportedEndpoints[lokiPath] {
		return false
	}
	// /loki/api/v1/detected_field/{name}/values -- unsupported
	if len(lokiPath) > len("/loki/api/v1/detected_field/") &&
		lokiPath[:len("/loki/api/v1/detected_field/")] == "/loki/api/v1/detected_field/" {
		return false
	}
	return true
}

// MapEndpoint returns the VictoriaLogs API path for the given Loki path
// and query kind. The queryKind distinguishes between log queries and
// stats/metric queries, which map to different VictoriaLogs endpoints.
//
// For label value paths like /loki/api/v1/label/{name}/values, pass
// the full path; the function matches by prefix.
//
// Returns the VictoriaLogs path and true if mapped, or ("", false) if
// the endpoint has no mapping (should have been caught by
// IsSupportedEndpoint first).
func MapEndpoint(lokiPath string, queryKind logsql.QueryKind) (string, bool) {
	// Label values: /loki/api/v1/label/{name}/values -> /select/logsql/field_values
	if isLabelValuesPath(lokiPath) {
		return VLogsPathFieldValues, true
	}

	switch lokiPath {
	case LokiPathQuery:
		if queryKind == logsql.QueryKindStats {
			return VLogsPathStatsQuery, true
		}
		return VLogsPathQuery, true

	case LokiPathQueryRange:
		if queryKind == logsql.QueryKindStats {
			return VLogsPathStatsQueryRange, true
		}
		return VLogsPathQuery, true

	case LokiPathLabels:
		return VLogsPathFieldNames, true

	case LokiPathSeries:
		return VLogsPathStreams, true

	case LokiPathIndexStats:
		return VLogsPathStatsQuery, true

	default:
		return "", false
	}
}

// BuildVLogsPath applies the optional tenant prefix to a VictoriaLogs
// API path. When tenant is empty, the path is returned as-is.
// Example: tenant="0:0", path="/select/logsql/query"
//
//	-> "/0:0/select/logsql/query"
func BuildVLogsPath(vlogsPath, tenant string) string {
	if tenant == "" {
		return vlogsPath
	}
	return "/" + tenant + vlogsPath
}

// isLabelValuesPath returns true if the path matches
// /loki/api/v1/label/{name}/values.
func isLabelValuesPath(path string) bool {
	return ExtractLabelName(path) != ""
}
