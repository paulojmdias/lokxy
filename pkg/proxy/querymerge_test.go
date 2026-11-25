package proxy

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsAdditiveVectorQuery(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  bool
	}{
		{name: "sum", query: `sum(count_over_time({app="api"}[15m]))`, want: true},
		{name: "sum rate", query: `sum(rate({app="api"}[15m]))`, want: true},
		{name: "sum bytes", query: `sum(bytes_over_time({app="api"}[15m]))`, want: true},
		{name: "sum with zero fill", query: `sum(count_over_time({app="api"}[15m])) or label_replace(vector(0), "computer", "dc-1", "__name__", ".*")`, want: true},
		{name: "sum with arbitrary or", query: `sum(count_over_time({app="api"}[15m])) or vector(1)`, want: false},
		{name: "vector", query: `vector(1)`, want: false},
		{name: "count over time", query: `count_over_time({app="api"}[15m])`, want: false},
		{name: "max", query: `max(count_over_time({app="api"}[15m]))`, want: false},
		{name: "avg", query: `avg(count_over_time({app="api"}[15m]))`, want: false},
		{name: "sum max", query: `sum(max_over_time({app="api"}[15m]))`, want: false},
		{name: "sum avg", query: `sum(avg_over_time({app="api"}[15m]))`, want: false},
		{name: "topk", query: `topk(5, count_over_time({app="api"}[15m]))`, want: false},
		{name: "invalid", query: `sum(`, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, isAdditiveVectorQuery(tt.query))
		})
	}
}
