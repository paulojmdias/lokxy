package handler

import (
	"time"

	"github.com/grafana/loki/v3/pkg/loghttp"
)

// Custom type that wraps []loghttp.Stream and implements ResultValue
type StreamResult []loghttp.Stream

func (sr StreamResult) Type() loghttp.ResultType {
	return loghttp.ResultTypeStream
}

type Entry struct {
	Timestamp time.Time `json:"ts"`
	Line      string    `json:"line"`
}

// LokiMetricStream represents a matrix or vector result for metrics queries
type LokiMetricStream struct {
	Metric map[string]string `json:"metric"`
	Value  []interface{}     `json:"value"`  // For vector results (single value per metric)
	Values [][]interface{}   `json:"values"` // For matrix results (multiple values over time)
}

// LokiMatrixStream represents a single matrix stream in Loki (used for range queries)
type LokiMatrixStream struct {
	Metric map[string]string `json:"metric"`
	Values [][]interface{}   `json:"values"` // Array of [timestamp, value] pairs
}
