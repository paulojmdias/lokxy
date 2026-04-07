package victorialogs

import (
	"testing"

	"github.com/VictoriaMetrics-Community/logql-to-logsql/lib/logsql"
	"github.com/stretchr/testify/require"
)

func TestIsSupportedEndpoint(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{"query", LokiPathQuery, true},
		{"query_range", LokiPathQueryRange, true},
		{"labels", LokiPathLabels, true},
		{"series", LokiPathSeries, true},
		{"index_stats", LokiPathIndexStats, true},
		{"label_values", "/loki/api/v1/label/hostname/values", true},

		// Unsupported endpoints.
		{"tail", LokiPathTail, false},
		{"patterns", LokiPathPatterns, false},
		{"detected_labels", LokiPathDetectedLabels, false},
		{"detected_fields", LokiPathDetectedFields, false},
		{"volume", LokiPathIndexVolume, false},
		{"volume_range", LokiPathIndexVolumeRange, false},
		{"detected_field_values", "/loki/api/v1/detected_field/method/values", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsSupportedEndpoint(tt.path)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestMapEndpoint(t *testing.T) {
	tests := []struct {
		name      string
		lokiPath  string
		queryKind logsql.QueryKind
		wantPath  string
		wantOK    bool
	}{
		// Log queries.
		{
			name:      "query log",
			lokiPath:  LokiPathQuery,
			queryKind: logsql.QueryKindLogs,
			wantPath:  VLogsPathQuery,
			wantOK:    true,
		},
		{
			name:      "query_range log",
			lokiPath:  LokiPathQueryRange,
			queryKind: logsql.QueryKindLogs,
			wantPath:  VLogsPathQuery,
			wantOK:    true,
		},

		// Stats queries.
		{
			name:      "query stats (instant)",
			lokiPath:  LokiPathQuery,
			queryKind: logsql.QueryKindStats,
			wantPath:  VLogsPathStatsQuery,
			wantOK:    true,
		},
		{
			name:      "query_range stats (range)",
			lokiPath:  LokiPathQueryRange,
			queryKind: logsql.QueryKindStats,
			wantPath:  VLogsPathStatsQueryRange,
			wantOK:    true,
		},

		// Metadata.
		{
			name:      "labels",
			lokiPath:  LokiPathLabels,
			queryKind: logsql.QueryKindLogs,
			wantPath:  VLogsPathFieldNames,
			wantOK:    true,
		},
		{
			name:      "series",
			lokiPath:  LokiPathSeries,
			queryKind: logsql.QueryKindLogs,
			wantPath:  VLogsPathStreams,
			wantOK:    true,
		},
		{
			name:      "index_stats",
			lokiPath:  LokiPathIndexStats,
			queryKind: logsql.QueryKindLogs,
			wantPath:  VLogsPathStatsQuery,
			wantOK:    true,
		},
		{
			name:      "label values",
			lokiPath:  "/loki/api/v1/label/hostname/values",
			queryKind: logsql.QueryKindLogs,
			wantPath:  VLogsPathFieldValues,
			wantOK:    true,
		},

		// Unmapped.
		{
			name:      "unknown path",
			lokiPath:  "/loki/api/v1/unknown",
			queryKind: logsql.QueryKindLogs,
			wantPath:  "",
			wantOK:    false,
		},
		{
			name:      "tail (unsupported)",
			lokiPath:  LokiPathTail,
			queryKind: logsql.QueryKindLogs,
			wantPath:  "",
			wantOK:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotPath, gotOK := MapEndpoint(tt.lokiPath, tt.queryKind)
			require.Equal(t, tt.wantOK, gotOK)
			require.Equal(t, tt.wantPath, gotPath)
		})
	}
}

func TestBuildVLogsPath(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		tenant string
		want   string
	}{
		{
			name:   "no tenant",
			path:   VLogsPathQuery,
			tenant: "",
			want:   "/select/logsql/query",
		},
		{
			name:   "with tenant",
			path:   VLogsPathQuery,
			tenant: "0:0",
			want:   "/0:0/select/logsql/query",
		},
		{
			name:   "tenant with stats",
			path:   VLogsPathStatsQueryRange,
			tenant: "1:2",
			want:   "/1:2/select/logsql/stats_query_range",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildVLogsPath(tt.path, tt.tenant)
			require.Equal(t, tt.want, got)
		})
	}
}
