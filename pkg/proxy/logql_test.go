package proxy

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRewriteRangeVector(t *testing.T) {
	tests := []struct {
		name        string
		query       string
		minInterval time.Duration
		wantRewrite bool
		wantErr     bool
		// wantContains is checked when rewrite is expected (AST round-trip may
		// change whitespace, so we verify key substrings rather than exact match).
		wantContains []string
	}{
		{
			name:         "rewrite small interval",
			query:        `count_over_time({app="foo"}[5s])`,
			minInterval:  1 * time.Minute,
			wantRewrite:  true,
			wantContains: []string{`count_over_time(`, `[1m]`},
		},
		{
			name:        "no rewrite when interval >= minInterval",
			query:       `count_over_time({app="foo"}[2m])`,
			minInterval: 1 * time.Minute,
			wantRewrite: false,
		},
		{
			name:         "rewrite with sum by wrapper",
			query:        `sum by (level, detected_level) (count_over_time({app="foo"}[5s]))`,
			minInterval:  1 * time.Minute,
			wantRewrite:  true,
			wantContains: []string{`count_over_time(`, `[1m]`, `sum by`},
		},
		{
			name:         "rewrite with line filter preserved",
			query:        `sum by (level) (count_over_time({app="foo"} |= "error" [5s]))`,
			minInterval:  1 * time.Minute,
			wantRewrite:  true,
			wantContains: []string{`count_over_time(`, `[1m]`, `|= "error"`},
		},
		{
			name:        "exact equal interval not rewritten",
			query:       `count_over_time({app="foo"}[1m])`,
			minInterval: 1 * time.Minute,
			wantRewrite: false,
		},
		{
			name:        "invalid query returns error",
			query:       `not a valid query {{{`,
			minInterval: 1 * time.Minute,
			wantRewrite: false,
			wantErr:     true,
		},
		{
			name:         "30s interval rewritten to 1m",
			query:        `sum by (level) (count_over_time({job="varlogs"}[30s]))`,
			minInterval:  1 * time.Minute,
			wantRewrite:  true,
			wantContains: []string{`[1m]`},
		},
		{
			name:        "larger interval not rewritten",
			query:       `count_over_time({app="foo"}[5m])`,
			minInterval: 1 * time.Minute,
			wantRewrite: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, rewritten, err := rewriteRangeVector(tt.query, tt.minInterval)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantRewrite, rewritten, "rewritten flag mismatch")
			if rewritten {
				for _, substr := range tt.wantContains {
					require.Contains(t, got, substr,
						"rewritten query should contain %q", substr)
				}
			} else {
				// When not rewritten, the original query should be returned unchanged.
				require.Equal(t, tt.query, got)
			}
		})
	}
}

func TestQueryHasLineFilters(t *testing.T) {
	tests := []struct {
		name       string
		query      string
		wantFilter bool
		wantErr    bool
	}{
		{
			name:       "no filters",
			query:      `count_over_time({app="foo"}[5s])`,
			wantFilter: false,
		},
		{
			name:       "line contains filter",
			query:      `count_over_time({app="foo"} |= "error" [5s])`,
			wantFilter: true,
		},
		{
			name:       "regex filter",
			query:      `count_over_time({app="foo"} |~ "err.*" [5s])`,
			wantFilter: true,
		},
		{
			name:       "negation filter",
			query:      `count_over_time({app="foo"} != "debug" [5s])`,
			wantFilter: true,
		},
		{
			name:       "negation regex filter",
			query:      `count_over_time({app="foo"} !~ "debug.*" [5s])`,
			wantFilter: true,
		},
		{
			name:       "with sum wrapper no filter",
			query:      `sum by (level) (count_over_time({app="foo"}[5s]))`,
			wantFilter: false,
		},
		{
			name:       "empty string filter is no-op",
			query:      "count_over_time({app=\"foo\"} |= `` [5s])",
			wantFilter: false,
		},
		{
			name:       "empty string filter with sum wrapper",
			query:      "sum by (level) (count_over_time({stack=\"observability\"} |= `` [5s]))",
			wantFilter: false,
		},
		{
			name:       "real filter after empty filter",
			query:      "count_over_time({app=\"foo\"} |= `` |= \"error\" [5s])",
			wantFilter: true,
		},
		{
			name:       "invalid query",
			query:      `{{{invalid`,
			wantFilter: false,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := queryHasLineFilters(tt.query)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantFilter, got)
		})
	}
}

func TestExtractStreamSelector(t *testing.T) {
	tests := []struct {
		name    string
		query   string
		wantSel string
		wantErr bool
	}{
		{
			name:  "simple selector",
			query: `count_over_time({app="foo"}[5s])`,
		},
		{
			name:  "multiple matchers",
			query: `count_over_time({app="foo", env="prod"}[5s])`,
		},
		{
			name:  "with sum by wrapper",
			query: `sum by (level) (count_over_time({app="foo", env="prod"}[5s]))`,
		},
		{
			name:    "invalid query",
			query:   `{{{invalid`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := extractStreamSelector(tt.query)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.NotEmpty(t, got)
			// The selector should start with { and end with }
			require.True(t, got[0] == '{', "selector should start with {")
			require.True(t, got[len(got)-1] == '}', "selector should end with }")
			// Should contain app matcher
			require.Contains(t, got, "app")
		})
	}
}

func TestParseStep(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    time.Duration
		wantErr bool
	}{
		{
			name:  "bare seconds integer",
			input: "5",
			want:  5 * time.Second,
		},
		{
			name:  "bare seconds large",
			input: "3600",
			want:  1 * time.Hour,
		},
		{
			name:  "bare seconds float",
			input: "0.5",
			want:  500 * time.Millisecond,
		},
		{
			name:  "prometheus duration minutes",
			input: "1m",
			want:  1 * time.Minute,
		},
		{
			name:  "prometheus duration hours",
			input: "1h",
			want:  1 * time.Hour,
		},
		{
			name:  "prometheus duration seconds",
			input: "30s",
			want:  30 * time.Second,
		},
		{
			name:  "prometheus duration composite",
			input: "1h30m",
			want:  90 * time.Minute,
		},
		{
			name:    "invalid string",
			input:   "notanumber",
			wantErr: true,
		},
		{
			name:    "empty string",
			input:   "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseStep(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got, "parsed duration mismatch")
		})
	}
}
