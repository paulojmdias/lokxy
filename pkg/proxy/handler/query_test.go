package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-kit/log"
	"github.com/stretchr/testify/require"
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

	results := make(chan *http.Response, 1)
	rec := httptest.NewRecorder()
	rec.WriteString(body)
	results <- rec.Result()
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

	results := make(chan *http.Response, 1)
	rec := httptest.NewRecorder()
	rec.WriteString(body)
	results <- rec.Result()
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

	results := make(chan *http.Response, 1)
	rec := httptest.NewRecorder()
	rec.WriteString(body)
	results <- rec.Result()
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

	results := make(chan *http.Response, len(responses))
	for _, respBody := range responses {
		rec := httptest.NewRecorder()
		rec.WriteString(respBody)
		results <- rec.Result()
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

	results := make(chan *http.Response, 1)
	rec := httptest.NewRecorder()
	rec.WriteString(body)
	results <- rec.Result()
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

	results := make(chan *http.Response, 1)
	rec := httptest.NewRecorder()
	rec.WriteString(body)
	results <- rec.Result()
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

	results := make(chan *http.Response, 1)
	rec := httptest.NewRecorder()
	rec.WriteString(body)
	results <- rec.Result()
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

	results := make(chan *http.Response, 1)
	rec := httptest.NewRecorder()
	rec.WriteString("invalid json")
	results <- rec.Result()
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

	results := make(chan *http.Response, 1)
	results <- &http.Response{
		StatusCode: 200,
		Body:       &failingQueryReader{},
	}
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

	results := make(chan *http.Response, 3)

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
	results <- rec1.Result()

	// Invalid JSON
	rec2 := httptest.NewRecorder()
	rec2.WriteString("invalid json")
	results <- rec2.Result()

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
	results <- rec3.Result()

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

	results := make(chan *http.Response)
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

	results := make(chan *http.Response, len(responses))
	for _, respBody := range responses {
		rec := httptest.NewRecorder()
		rec.WriteString(respBody)
		results <- rec.Result()
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

// failingQueryReader always fails on Read (simulates network/IO failure)
type failingQueryReader struct{}

func (f *failingQueryReader) Read([]byte) (int, error) {
	return 0, errors.New("read error")
}

func (f *failingQueryReader) Close() error {
	return nil
}
