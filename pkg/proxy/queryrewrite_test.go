package proxy

import "testing"

func TestRewriteMetadataQuery(t *testing.T) {
	metadataFields := []string{"instance", "trace_id"}

	tests := []struct {
		name  string
		query string
		mode  rewriteMode
		want  string
	}{
		{
			name:  "move single metadata matcher to pipeline",
			query: `{stack="core", instance="host1"} |= ""`,
			mode:  rewriteMoveToPipeline,
			want:  `{stack="core"} | instance="host1" |= ""`,
		},
		{
			name:  "move multiple metadata matchers to pipeline",
			query: `{stack="core", instance="host1", trace_id="abc123"}`,
			mode:  rewriteMoveToPipeline,
			want:  `{stack="core"} | instance="host1" | trace_id="abc123"`,
		},
		{
			name:  "strip metadata matcher",
			query: `{stack="core", instance="host1"}`,
			mode:  rewriteStrip,
			want:  `{stack="core"}`,
		},
		{
			name:  "no metadata matchers - unchanged",
			query: `{stack="core", role="app"}`,
			mode:  rewriteMoveToPipeline,
			want:  `{stack="core", role="app"}`,
		},
		{
			name:  "all matchers are metadata - unchanged (would leave empty selector)",
			query: `{instance="host1"}`,
			mode:  rewriteMoveToPipeline,
			want:  `{instance="host1"}`,
		},
		{
			name:  "regex matcher on metadata field",
			query: `{stack="core", instance=~"host.*"}`,
			mode:  rewriteMoveToPipeline,
			want:  `{stack="core"} | instance=~"host.*"`,
		},
		{
			name:  "not-equal matcher on metadata field",
			query: `{stack="core", instance!="host1"}`,
			mode:  rewriteMoveToPipeline,
			want:  `{stack="core"} | instance!="host1"`,
		},
		{
			name:  "empty query - unchanged",
			query: "",
			mode:  rewriteMoveToPipeline,
			want:  "",
		},
		{
			name:  "no fields configured - unchanged",
			query: `{stack="core", instance="host1"}`,
			mode:  rewriteMoveToPipeline,
			want:  `{stack="core", instance="host1"}`,
		},
		{
			name:  "metric query with metadata matcher",
			query: `sum by (level) (count_over_time({stack="core", instance="host1"} |= "" [1m]))`,
			mode:  rewriteMoveToPipeline,
			want:  `sum by (level) (count_over_time({stack="core"} | instance="host1" |= "" [1m]))`,
		},
		{
			name:  "value with escaped quotes",
			query: `{stack="core", instance="host\"1"}`,
			mode:  rewriteMoveToPipeline,
			want:  `{stack="core"} | instance="host\"1"`,
		},
		{
			name:  "value with comma inside quotes",
			query: `{stack="core", instance="a,b"}`,
			mode:  rewriteMoveToPipeline,
			want:  `{stack="core"} | instance="a,b"`,
		},
		{
			name:  "backtick quoted value",
			query: "{stack=`core`, instance=`host1`}",
			mode:  rewriteMoveToPipeline,
			want:  "{stack=`core`} | instance=`host1`",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fields := metadataFields
			if tt.name == "no fields configured - unchanged" {
				fields = nil
			}
			got := rewriteMetadataQuery(tt.query, fields, tt.mode)
			if got != tt.want {
				t.Errorf("rewriteMetadataQuery(%q, %v, %d)\n  got:  %q\n  want: %q",
					tt.query, fields, tt.mode, got, tt.want)
			}
		})
	}
}
