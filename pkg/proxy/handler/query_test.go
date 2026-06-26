package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-kit/log"
	"github.com/prometheus/common/model"
	"github.com/stretchr/testify/require"

	"github.com/paulojmdias/lokxy/pkg/proxy/proxyresponse"
)

func TestHandleLokiQueries_StreamResult(t *testing.T) {
	logger := log.NewNopLogger()

	body := `{
		"status": "success",
		"data": {
			"resultType": "streams",
			"result": [
				{
					"stream": {"app": "nginx", "environment": "prod"},
					"values": [
						["1609459200000000000", "log line 1"],
						["1609459201000000000", "log line 2"]
					]
				}
			],
			"stats": {
				"summary": {
					"bytesProcessedPerSecond": 1024,
					"linesProcessedPerSecond": 100,
					"totalBytesProcessed": 10240,
					"totalLinesProcessed": 1000,
					"execTime": 0.1
				}
			}
		}
	}`

	results := make(chan *proxyresponse.BackendResponse, 1)
	rec := httptest.NewRecorder()
	rec.WriteString(body)
	results <- wrapResponse(rec.Result())
	close(results)

	w := httptest.NewRecorder()
	HandleLokiQueries(t.Context(), w, results, nil, logger)

	var response map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

	require.Equal(t, "success", response["status"])
	data, ok := response["data"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "streams", data["resultType"])

	result, ok := data["result"].([]any)
	require.True(t, ok)
	require.Len(t, result, 1)
}

func TestHandleLokiQueries_MatrixResult(t *testing.T) {
	logger := log.NewNopLogger()

	body := `{
		"status": "success",
		"data": {
			"resultType": "matrix",
			"result": [
				{
					"metric": {"__name__": "bytes_rate", "app": "nginx"},
					"values": [
						[1609459200, "1024"],
						[1609459260, "2048"]
					]
				}
			],
			"stats": {}
		}
	}`

	results := make(chan *proxyresponse.BackendResponse, 1)
	rec := httptest.NewRecorder()
	rec.WriteString(body)
	results <- wrapResponse(rec.Result())
	close(results)

	w := httptest.NewRecorder()
	HandleLokiQueries(t.Context(), w, results, nil, logger)

	var response map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

	require.Equal(t, "success", response["status"])
	data, ok := response["data"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "matrix", data["resultType"])

	result, ok := data["result"].([]any)
	require.True(t, ok)
	require.Len(t, result, 1)
}

func TestHandleLokiQueries_VectorResult(t *testing.T) {
	logger := log.NewNopLogger()

	body := `{
		"status": "success",
		"data": {
			"resultType": "vector",
			"result": [
				{
					"metric": {"app": "nginx"},
					"value": [1609459200, "100"]
				},
				{
					"metric": {"app": "api"},
					"value": [1609459200, "50"]
				}
			],
			"stats": {}
		}
	}`

	results := make(chan *proxyresponse.BackendResponse, 1)
	rec := httptest.NewRecorder()
	rec.WriteString(body)
	results <- wrapResponse(rec.Result())
	close(results)

	w := httptest.NewRecorder()
	HandleLokiQueries(t.Context(), w, results, nil, logger)

	var response map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

	require.Equal(t, "success", response["status"])
	data, ok := response["data"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "vector", data["resultType"])

	result, ok := data["result"].([]any)
	require.True(t, ok)
	require.Len(t, result, 2)
}

func TestHandleLokiQueries_MultipleStreamResponses(t *testing.T) {
	logger := log.NewNopLogger()

	responses := []string{
		`{
			"status": "success",
			"data": {
				"resultType": "streams",
				"result": [
					{
						"stream": {"app": "nginx"},
						"values": [["1609459200000000000", "log 1"]]
					}
				],
				"stats": {}
			}
		}`,
		`{
			"status": "success",
			"data": {
				"resultType": "streams",
				"result": [
					{
						"stream": {"app": "api"},
						"values": [["1609459201000000000", "log 2"]]
					}
				],
				"stats": {}
			}
		}`,
	}

	results := make(chan *proxyresponse.BackendResponse, len(responses))
	for _, respBody := range responses {
		rec := httptest.NewRecorder()
		rec.WriteString(respBody)
		results <- wrapResponse(rec.Result())
	}
	close(results)

	w := httptest.NewRecorder()
	HandleLokiQueries(t.Context(), w, results, nil, logger)

	var response map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

	data, ok := response["data"].(map[string]any)
	require.True(t, ok)

	result, ok := data["result"].([]any)
	require.True(t, ok)
	// Should merge streams from both backends
	require.Len(t, result, 2)
}

func TestHandleLokiQueries_WithEncodingFlags(t *testing.T) {
	logger := log.NewNopLogger()

	body := `{
		"status": "success",
		"data": {
			"resultType": "streams",
			"result": [],
			"stats": {},
			"encodingFlags": ["gzip", "snappy"]
		}
	}`

	results := make(chan *proxyresponse.BackendResponse, 1)
	rec := httptest.NewRecorder()
	rec.WriteString(body)
	results <- wrapResponse(rec.Result())
	close(results)

	w := httptest.NewRecorder()
	HandleLokiQueries(t.Context(), w, results, nil, logger)

	var response map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

	data, ok := response["data"].(map[string]any)
	require.True(t, ok)

	encodingFlags, ok := data["encodingFlags"].([]any)
	require.True(t, ok)
	require.Len(t, encodingFlags, 2)
}

func TestHandleLokiQueries_WithStructuredMetadata(t *testing.T) {
	logger := log.NewNopLogger()

	body := `{
		"status": "success",
		"data": {
			"resultType": "streams",
			"result": [
				{
					"stream": {"app": "nginx"},
					"values": [
						["1609459200000000000", "log line", {"structuredMetadata": [{"key": "value"}]}]
					]
				}
			],
			"stats": {}
		}
	}`

	results := make(chan *proxyresponse.BackendResponse, 1)
	rec := httptest.NewRecorder()
	rec.WriteString(body)
	results <- wrapResponse(rec.Result())
	close(results)

	w := httptest.NewRecorder()
	HandleLokiQueries(t.Context(), w, results, nil, logger)

	var response map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

	require.Equal(t, "success", response["status"])
	data, ok := response["data"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "streams", data["resultType"])
}

func TestHandleLokiQueries_EmptyResult(t *testing.T) {
	logger := log.NewNopLogger()

	body := `{
		"status": "success",
		"data": {
			"resultType": "streams",
			"result": [],
			"stats": {}
		}
	}`

	results := make(chan *proxyresponse.BackendResponse, 1)
	rec := httptest.NewRecorder()
	rec.WriteString(body)
	results <- wrapResponse(rec.Result())
	close(results)

	w := httptest.NewRecorder()
	HandleLokiQueries(t.Context(), w, results, nil, logger)

	var response map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

	require.Equal(t, "success", response["status"])
	data, ok := response["data"].(map[string]any)
	require.True(t, ok)

	// Result can be empty array or null
	if result, ok := data["result"].([]any); ok {
		require.Empty(t, result)
	}
}

func TestHandleLokiQueries_InvalidJSON(t *testing.T) {
	logger := log.NewNopLogger()

	results := make(chan *proxyresponse.BackendResponse, 1)
	rec := httptest.NewRecorder()
	rec.WriteString("invalid json")
	results <- wrapResponse(rec.Result())
	close(results)

	w := httptest.NewRecorder()
	HandleLokiQueries(t.Context(), w, results, nil, logger)

	var response map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

	// Should return success with empty result on error
	require.Equal(t, "success", response["status"])
	data, ok := response["data"].(map[string]any)
	require.True(t, ok)

	result, ok := data["result"].([]any)
	require.True(t, ok)
	require.Empty(t, result)
}

func TestHandleLokiQueries_ResponseReaderError(t *testing.T) {
	logger := log.NewNopLogger()

	results := make(chan *proxyresponse.BackendResponse, 1)
	results <- wrapResponse(&http.Response{
		StatusCode: 200,
		Body:       &failingQueryReader{},
	})
	close(results)

	w := httptest.NewRecorder()
	HandleLokiQueries(t.Context(), w, results, nil, logger)

	var response map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

	// Should return success with empty result on read error
	require.Equal(t, "success", response["status"])
	data, ok := response["data"].(map[string]any)
	require.True(t, ok)

	result, ok := data["result"].([]any)
	require.True(t, ok)
	require.Empty(t, result)
}

func TestHandleLokiQueries_PartialFailure(t *testing.T) {
	logger := log.NewNopLogger()

	results := make(chan *proxyresponse.BackendResponse, 3)

	// Valid response
	rec1 := httptest.NewRecorder()
	rec1.WriteString(`{
		"status": "success",
		"data": {
			"resultType": "streams",
			"result": [{"stream": {"app": "nginx"}, "values": []}],
			"stats": {}
		}
	}`)
	results <- wrapResponse(rec1.Result())

	// Invalid JSON
	rec2 := httptest.NewRecorder()
	rec2.WriteString("invalid json")
	results <- wrapResponse(rec2.Result())

	// Valid response
	rec3 := httptest.NewRecorder()
	rec3.WriteString(`{
		"status": "success",
		"data": {
			"resultType": "streams",
			"result": [{"stream": {"app": "api"}, "values": []}],
			"stats": {}
		}
	}`)
	results <- wrapResponse(rec3.Result())

	close(results)

	w := httptest.NewRecorder()
	HandleLokiQueries(t.Context(), w, results, nil, logger)

	var response map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

	data, ok := response["data"].(map[string]any)
	require.True(t, ok)

	result, ok := data["result"].([]any)
	require.True(t, ok)
	// Should have results from 2 successful responses
	require.Len(t, result, 2)
}

func TestHandleLokiQueries_NoResponses(t *testing.T) {
	logger := log.NewNopLogger()

	results := make(chan *proxyresponse.BackendResponse)
	close(results)

	w := httptest.NewRecorder()
	HandleLokiQueries(t.Context(), w, results, nil, logger)

	var response map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

	require.Equal(t, "success", response["status"])
	data, ok := response["data"].(map[string]any)
	require.True(t, ok)

	result, ok := data["result"].([]any)
	require.True(t, ok)
	require.Empty(t, result)
}

func TestHandleLokiQueries_MultipleEncodingFlagsDeduplication(t *testing.T) {
	logger := log.NewNopLogger()

	responses := []string{
		`{
			"status": "success",
			"data": {
				"resultType": "streams",
				"result": [],
				"stats": {},
				"encodingFlags": ["gzip", "snappy"]
			}
		}`,
		`{
			"status": "success",
			"data": {
				"resultType": "streams",
				"result": [],
				"stats": {},
				"encodingFlags": ["gzip", "zstd"]
			}
		}`,
	}

	results := make(chan *proxyresponse.BackendResponse, len(responses))
	for _, respBody := range responses {
		rec := httptest.NewRecorder()
		rec.WriteString(respBody)
		results <- wrapResponse(rec.Result())
	}
	close(results)

	w := httptest.NewRecorder()
	HandleLokiQueries(t.Context(), w, results, nil, logger)

	var response map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

	data, ok := response["data"].(map[string]any)
	require.True(t, ok)

	encodingFlags, ok := data["encodingFlags"].([]any)
	require.True(t, ok)
	// Should have unique flags: gzip, snappy, zstd
	require.Len(t, encodingFlags, 3)
}

func TestHandleLokiQueries_NoEncodingFlags(t *testing.T) {
	logger := log.NewNopLogger()

	body := `{
		"status": "success",
		"data": {
			"resultType": "streams",
			"result": [
				{"stream": {"app":"test"}, "values": [["1700000000000000000","line1"]]}
			],
			"stats": {}
		}
	}`

	results := make(chan *proxyresponse.BackendResponse, 1)
	rec := httptest.NewRecorder()
	rec.WriteString(body)
	results <- wrapResponse(rec.Result())
	close(results)

	w := httptest.NewRecorder()
	HandleLokiQueries(t.Context(), w, results, nil, logger)

	var got map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	data, ok := got["data"].(map[string]any)
	require.True(t, ok)
	_, hasFlags := data["encodingFlags"]
	require.False(t, hasFlags, "encodingFlags should not appear when absent from upstream")
}

func TestEncodingFlagsEnvelope_Unmarshal(t *testing.T) {
	body := []byte(`{"data":{"encodingFlags":["categorize-labels","flag2"],"resultType":"streams"}}`)
	var envelope encodingFlagsEnvelope
	require.NoError(t, json.Unmarshal(body, &envelope))
	require.Equal(t, []string{"categorize-labels", "flag2"}, envelope.Data.EncodingFlags)
}

func TestEncodingFlagsEnvelope_NoFlags(t *testing.T) {
	body := []byte(`{"data":{"resultType":"streams"}}`)
	var envelope encodingFlagsEnvelope
	require.NoError(t, json.Unmarshal(body, &envelope))
	require.Empty(t, envelope.Data.EncodingFlags)
}

func TestHandleLokiQueries_EmptyBody(t *testing.T) {
	logger := log.NewNopLogger()

	// Send a response with an empty JSON object — no recognised result type.
	results := make(chan *proxyresponse.BackendResponse, 1)
	rec := httptest.NewRecorder()
	rec.WriteString(`{}`)
	results <- wrapResponse(rec.Result())
	close(results)

	w := httptest.NewRecorder()
	HandleLokiQueries(t.Context(), w, results, nil, logger)

	// Should still produce a valid (empty) encoded response
	require.NotEmpty(t, w.Body.Bytes())
}

// failingQueryReader always fails on Read (simulates network/IO failure)
type failingQueryReader struct{}

func (f *failingQueryReader) Read([]byte) (int, error) {
	return 0, errors.New("read error")
}

func (f *failingQueryReader) Close() error {
	return nil
}

// TestHandleLokiQueries_VectorSumMerge verifies that vector entries with the
// same metric labels from multiple backends are summed into a single entry.
// This is the exact scenario that triggers the Grafana health-check failure:
// each backend returns {} -> 2 for "vector(1)+vector(1)".
func TestHandleLokiQueries_VectorSumMerge(t *testing.T) {
	logger := log.NewNopLogger()

	// Two backends returning the same metric with the same value (health-check scenario)
	responses := []string{
		`{
			"status": "success",
			"data": {
				"resultType": "vector",
				"result": [
					{"metric": {}, "value": [1609459200, "2"]}
				],
				"stats": {}
			}
		}`,
		`{
			"status": "success",
			"data": {
				"resultType": "vector",
				"result": [
					{"metric": {}, "value": [1609459200, "2"]}
				],
				"stats": {}
			}
		}`,
	}

	results := make(chan *proxyresponse.BackendResponse, len(responses))
	for _, respBody := range responses {
		rec := httptest.NewRecorder()
		rec.WriteString(respBody)
		results <- wrapResponse(rec.Result())
	}
	close(results)

	w := httptest.NewRecorder()
	HandleLokiQueries(t.Context(), w, results, nil, logger)

	var response map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

	data, ok := response["data"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "vector", data["resultType"])

	result, ok := data["result"].([]any)
	require.True(t, ok)
	// Must be 1 entry (merged), not 2 (which breaks Grafana health-check)
	require.Len(t, result, 1)

	// Value should be deduplicated (kept from first backend), not summed
	entry := result[0].(map[string]any)
	value := entry["value"].([]any)
	// model.SampleValue serializes as a JSON string via its MarshalJSON
	require.Equal(t, "2", value[1])
}

// TestHandleLokiQueries_VectorSumMergeDifferentMetrics verifies that vector
// entries with different metric labels are kept separate (not merged).
func TestHandleLokiQueries_VectorSumMergeDifferentMetrics(t *testing.T) {
	logger := log.NewNopLogger()

	responses := []string{
		`{
			"status": "success",
			"data": {
				"resultType": "vector",
				"result": [
					{"metric": {"app": "nginx"}, "value": [1609459200, "100"]}
				],
				"stats": {}
			}
		}`,
		`{
			"status": "success",
			"data": {
				"resultType": "vector",
				"result": [
					{"metric": {"app": "api"}, "value": [1609459200, "50"]}
				],
				"stats": {}
			}
		}`,
	}

	results := make(chan *proxyresponse.BackendResponse, len(responses))
	for _, respBody := range responses {
		rec := httptest.NewRecorder()
		rec.WriteString(respBody)
		results <- wrapResponse(rec.Result())
	}
	close(results)

	w := httptest.NewRecorder()
	HandleLokiQueries(t.Context(), w, results, nil, logger)

	var response map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

	data, ok := response["data"].(map[string]any)
	require.True(t, ok)

	result, ok := data["result"].([]any)
	require.True(t, ok)
	// Different metrics should remain separate
	require.Len(t, result, 2)
}

// TestHandleLokiQueries_MatrixSumMerge verifies that matrix entries with the
// same metric labels are merged, summing values at the same timestamps.
func TestHandleLokiQueries_MatrixSumMerge(t *testing.T) {
	logger := log.NewNopLogger()

	responses := []string{
		`{
			"status": "success",
			"data": {
				"resultType": "matrix",
				"result": [
					{
						"metric": {"app": "nginx"},
						"values": [[1609459200, "100"], [1609459260, "200"]]
					}
				],
				"stats": {}
			}
		}`,
		`{
			"status": "success",
			"data": {
				"resultType": "matrix",
				"result": [
					{
						"metric": {"app": "nginx"},
						"values": [[1609459200, "50"], [1609459320, "300"]]
					}
				],
				"stats": {}
			}
		}`,
	}

	results := make(chan *proxyresponse.BackendResponse, len(responses))
	for _, respBody := range responses {
		rec := httptest.NewRecorder()
		rec.WriteString(respBody)
		results <- wrapResponse(rec.Result())
	}
	close(results)

	w := httptest.NewRecorder()
	HandleLokiQueries(t.Context(), w, results, nil, logger)

	var response map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

	data, ok := response["data"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "matrix", data["resultType"])

	result, ok := data["result"].([]any)
	require.True(t, ok)
	// Same metric from 2 backends should merge into 1 entry
	require.Len(t, result, 1)

	entry := result[0].(map[string]any)
	values := entry["values"].([]any)
	// 3 unique timestamps: 1609459200 (summed), 1609459260, 1609459320
	require.Len(t, values, 3)

	// First timestamp: 100 + 50 = 150
	ts0 := values[0].([]any)
	// model.SampleValue serializes as a JSON string via its MarshalJSON
	require.Equal(t, "150", ts0[1])
}

// TestModelMetricKey verifies key generation for metric label aggregation.
func TestModelMetricKey(t *testing.T) {
	tests := []struct {
		name     string
		metric   model.Metric
		expected string
	}{
		{"empty", model.Metric{}, ""},
		{"single", model.Metric{"app": "nginx"}, "app=nginx"},
		{"multiple sorted", model.Metric{"app": "nginx", "env": "prod"}, "app=nginx,env=prod"},
		{"order independent", model.Metric{"env": "prod", "app": "nginx"}, "app=nginx,env=prod"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, modelMetricKey(tt.metric))
		})
	}
}

// TestSumMergeSamplePairs verifies timestamp-based sum-merge for matrix values.
func TestSumMergeSamplePairs(t *testing.T) {
	existing := []model.SamplePair{
		{Timestamp: 1000, Value: 100},
		{Timestamp: 2000, Value: 200},
	}
	incoming := []model.SamplePair{
		{Timestamp: 1000, Value: 50},
		{Timestamp: 3000, Value: 300},
	}

	merged := sumMergeSamplePairs(existing, incoming)

	require.Len(t, merged, 3)
	// Sorted by timestamp
	require.Equal(t, model.Time(1000), merged[0].Timestamp)
	require.InDelta(t, float64(150), float64(merged[0].Value), 0.001) // 100 + 50
	require.Equal(t, model.Time(2000), merged[1].Timestamp)
	require.InDelta(t, float64(200), float64(merged[1].Value), 0.001)
	require.Equal(t, model.Time(3000), merged[2].Timestamp)
	require.InDelta(t, float64(300), float64(merged[2].Value), 0.001)
}

// TestSumMergeSamplePairs_EmptyInputs verifies edge cases for empty slices.
func TestSumMergeSamplePairs_EmptyInputs(t *testing.T) {
	pairs := []model.SamplePair{{Timestamp: 1000, Value: 100}}

	require.Equal(t, pairs, sumMergeSamplePairs(nil, pairs))
	require.Equal(t, pairs, sumMergeSamplePairs(pairs, nil))
}
