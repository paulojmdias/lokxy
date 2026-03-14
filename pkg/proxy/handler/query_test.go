package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/go-kit/log"
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
	HandleLokiQueries(t.Context(), w, results, logger)

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
	HandleLokiQueries(t.Context(), w, results, logger)

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
	HandleLokiQueries(t.Context(), w, results, logger)

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
	HandleLokiQueries(t.Context(), w, results, logger)

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
	HandleLokiQueries(t.Context(), w, results, logger)

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
	HandleLokiQueries(t.Context(), w, results, logger)

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
	HandleLokiQueries(t.Context(), w, results, logger)

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
	HandleLokiQueries(t.Context(), w, results, logger)

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
	HandleLokiQueries(t.Context(), w, results, logger)

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
	HandleLokiQueries(t.Context(), w, results, logger)

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
	HandleLokiQueries(t.Context(), w, results, logger)

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
	HandleLokiQueries(t.Context(), w, results, logger)

	var response map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

	data, ok := response["data"].(map[string]any)
	require.True(t, ok)

	encodingFlags, ok := data["encodingFlags"].([]any)
	require.True(t, ok)
	// Should have unique flags: gzip, snappy, zstd
	require.Len(t, encodingFlags, 3)
}

func TestHandleLokiQueries_MatrixDownsampledWhenStepOverride(t *testing.T) {
	logger := log.NewNopLogger()

	// 7 data points at 10s intervals: t=0,10,20,30,40,50,60 seconds (Unix ms)
	body := `{
		"status": "success",
		"data": {
			"resultType": "matrix",
			"result": [
				{
					"metric": {"app": "nginx"},
					"values": [
						[0,   "1"],
						[10,  "2"],
						[20,  "3"],
						[30,  "4"],
						[40,  "5"],
						[50,  "6"],
						[60,  "7"]
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

	// OriginalStep (60s) > ConfiguredStep (10s): downsampling should occur
	ctx := WithStepInfo(t.Context(), StepInfo{
		OriginalStep:   60 * time.Second,
		ConfiguredStep: 10 * time.Second,
	})

	w := httptest.NewRecorder()
	HandleLokiQueries(ctx, w, results, logger)

	var response map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

	data, ok := response["data"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "matrix", data["resultType"])

	result, ok := data["result"].([]any)
	require.True(t, ok)
	require.Len(t, result, 1)

	series, ok := result[0].(map[string]any)
	require.True(t, ok)
	values, ok := series["values"].([]any)
	require.True(t, ok)
	// 7 points at 10s intervals, bucketed into 60s: t=0..59 → bucket 0, t=60 → bucket 60 → 2 buckets
	require.Less(t, len(values), 7, "downsampling should reduce the number of data points")
}

func TestHandleLokiQueries_MatrixNotDownsampledWhenNoStepInfo(t *testing.T) {
	logger := log.NewNopLogger()

	body := `{
		"status": "success",
		"data": {
			"resultType": "matrix",
			"result": [
				{
					"metric": {"app": "nginx"},
					"values": [
						[0,  "1"],
						[30, "2"],
						[60, "3"]
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

	// No StepInfo in context: no downsampling
	w := httptest.NewRecorder()
	HandleLokiQueries(t.Context(), w, results, logger)

	var response map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

	data, ok := response["data"].(map[string]any)
	require.True(t, ok)

	result, ok := data["result"].([]any)
	require.True(t, ok)
	require.Len(t, result, 1)

	series, ok := result[0].(map[string]any)
	require.True(t, ok)
	values, ok := series["values"].([]any)
	require.True(t, ok)
	require.Len(t, values, 3, "all 3 original points should be preserved")
}

func TestHandleLokiQueries_MatrixNotDownsampledWhenOriginalStepNotLarger(t *testing.T) {
	logger := log.NewNopLogger()

	body := `{
		"status": "success",
		"data": {
			"resultType": "matrix",
			"result": [
				{
					"metric": {"app": "nginx"},
					"values": [
						[0,  "1"],
						[30, "2"],
						[60, "3"]
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

	// OriginalStep (10s) <= ConfiguredStep (60s): no downsampling should occur
	ctx := WithStepInfo(t.Context(), StepInfo{
		OriginalStep:   10 * time.Second,
		ConfiguredStep: 60 * time.Second,
	})

	w := httptest.NewRecorder()
	HandleLokiQueries(ctx, w, results, logger)

	var response map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

	data, ok := response["data"].(map[string]any)
	require.True(t, ok)

	result, ok := data["result"].([]any)
	require.True(t, ok)
	require.Len(t, result, 1)

	series, ok := result[0].(map[string]any)
	require.True(t, ok)
	values, ok := series["values"].([]any)
	require.True(t, ok)
	require.Len(t, values, 3, "all 3 original points should be preserved when OriginalStep <= ConfiguredStep")
}

func TestHandleLokiQueries_AggregateMatrixSum(t *testing.T) {
	logger := log.NewNopLogger()

	// 6 data points at 1-minute intervals (millisecond timestamps).
	// Bucket 0ms (minutes 0-2): values 10+20+30=60
	// Bucket 180000ms (minutes 3-5): values 40+50+60=150
	body := `{
		"status": "success",
		"data": {
			"resultType": "matrix",
			"result": [
				{
					"metric": {"level": "info"},
					"values": [
						[0,   "10"],
						[60,  "20"],
						[120, "30"],
						[180, "40"],
						[240, "50"],
						[300, "60"]
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

	// OriginalStep=3m (180s), ConfiguredStep=1m (60s), AggregateSum=true
	ctx := WithStepInfo(t.Context(), StepInfo{
		OriginalStep:   3 * time.Minute,
		ConfiguredStep: 1 * time.Minute,
		AggregateSum:   true,
	})

	w := httptest.NewRecorder()
	HandleLokiQueries(ctx, w, results, logger)

	var response map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

	data, ok := response["data"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "matrix", data["resultType"])

	result, ok := data["result"].([]any)
	require.True(t, ok)
	require.Len(t, result, 1)

	series, ok := result[0].(map[string]any)
	require.True(t, ok)
	values, ok := series["values"].([]any)
	require.True(t, ok)
	// 6 one-minute points aggregated into 3-minute buckets -> 2 buckets
	require.Len(t, values, 2, "6 points at 1m should aggregate into 2 x 3m buckets")

	// First bucket: sum of 10+20+30=60
	bucket0 := values[0].([]any)
	v0, err := strconv.ParseFloat(bucket0[1].(string), 64)
	require.NoError(t, err)
	require.InDelta(t, 60.0, v0, 0.01, "first bucket sum should be 60")

	// Second bucket: sum of 40+50+60=150
	bucket1 := values[1].([]any)
	v1, err := strconv.ParseFloat(bucket1[1].(string), 64)
	require.NoError(t, err)
	require.InDelta(t, 150.0, v1, 0.01, "second bucket sum should be 150")
}

func TestHandleLokiQueries_AggregateMatrixSum_MultipleSeries(t *testing.T) {
	logger := log.NewNopLogger()

	// Two series (info, error), each with 4 points at 1-minute intervals.
	// Aggregation step: 2 minutes -> 2 buckets per series.
	body := `{
		"status": "success",
		"data": {
			"resultType": "matrix",
			"result": [
				{
					"metric": {"level": "info"},
					"values": [
						[0,   "100"],
						[60,  "200"],
						[120, "300"],
						[180, "400"]
					]
				},
				{
					"metric": {"level": "error"},
					"values": [
						[0,   "5"],
						[60,  "10"],
						[120, "15"],
						[180, "20"]
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

	ctx := WithStepInfo(t.Context(), StepInfo{
		OriginalStep:   2 * time.Minute,
		ConfiguredStep: 1 * time.Minute,
		AggregateSum:   true,
	})

	w := httptest.NewRecorder()
	HandleLokiQueries(ctx, w, results, logger)

	var response map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

	data := response["data"].(map[string]any)
	result := data["result"].([]any)
	require.Len(t, result, 2, "should preserve both series")

	// Each series should have 2 buckets
	for i, r := range result {
		series := r.(map[string]any)
		values := series["values"].([]any)
		require.Len(t, values, 2, "series %d should have 2 aggregated buckets", i)
	}

	// info series: bucket0=100+200=300, bucket1=300+400=700
	infoValues := result[0].(map[string]any)["values"].([]any)
	iv0, err := strconv.ParseFloat(infoValues[0].([]any)[1].(string), 64)
	require.NoError(t, err)
	require.InDelta(t, 300.0, iv0, 0.01)
	iv1, err := strconv.ParseFloat(infoValues[1].([]any)[1].(string), 64)
	require.NoError(t, err)
	require.InDelta(t, 700.0, iv1, 0.01)

	// error series: bucket0=5+10=15, bucket1=15+20=35
	errorValues := result[1].(map[string]any)["values"].([]any)
	ev0, err := strconv.ParseFloat(errorValues[0].([]any)[1].(string), 64)
	require.NoError(t, err)
	require.InDelta(t, 15.0, ev0, 0.01)
	ev1, err := strconv.ParseFloat(errorValues[1].([]any)[1].(string), 64)
	require.NoError(t, err)
	require.InDelta(t, 35.0, ev1, 0.01)
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
	HandleLokiQueries(t.Context(), w, results, logger)

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
