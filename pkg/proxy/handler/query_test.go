package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

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

func TestHandleLokiQueries_ScalarResult(t *testing.T) {
	logger := log.NewNopLogger()

	body := `{
		"status": "success",
		"data": {
			"resultType": "scalar",
			"result": [1609459200, "1"],
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

	data, ok := response["data"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "scalar", data["resultType"])

	// scalar should be forwarded as a JSON array [ts,value]
	result, ok := data["result"].([]any)
	require.True(t, ok)
	require.Len(t, result, 2)
	require.Equal(t, "1", result[1])
}

func TestHandleLokiQueries_MultipleScalarResponses_DoesNotOverrideFirst(t *testing.T) {
	logger := log.NewNopLogger()

	results := make(chan *proxyresponse.BackendResponse, 2)

	rec1 := httptest.NewRecorder()
	rec1.WriteString(`{
		"status": "success",
		"data": {
			"resultType": "scalar",
			"result": [1609459200, "1"],
			"stats": {}
		}
	}`)
	results <- wrapResponse(rec1.Result())

	rec2 := httptest.NewRecorder()
	rec2.WriteString(`{
		"status": "success",
		"data": {
			"resultType": "scalar",
			"result": [1609459200, "2"],
			"stats": {}
		}
	}`)
	results <- wrapResponse(rec2.Result())
	close(results)

	w := httptest.NewRecorder()
	HandleLokiQueries(t.Context(), w, results, logger)

	var response map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

	data, ok := response["data"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "scalar", data["resultType"])

	result, ok := data["result"].([]any)
	require.True(t, ok)
	require.Len(t, result, 2)
	require.Equal(t, "1", result[1])
}

func TestHandleLokiQueries_NullResultBecomesEmptyArray(t *testing.T) {
	logger := log.NewNopLogger()

	body := `{
		"status": "success",
		"data": {
			"resultType": "streams",
			"result": null,
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

	data, ok := response["data"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "streams", data["resultType"])

	result, ok := data["result"].([]any)
	require.True(t, ok)
	require.Empty(t, result)
}

func TestHandleLokiQueries_NullMatrixResultBecomesEmptyArray(t *testing.T) {
	logger := log.NewNopLogger()

	body := `{
		"status": "success",
		"data": {
			"resultType": "matrix",
			"result": null,
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

	data, ok := response["data"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "matrix", data["resultType"])

	result, ok := data["result"].([]any)
	require.True(t, ok)
	require.Empty(t, result)
}

func TestHandleLokiQueries_NullVectorResultBecomesEmptyArray(t *testing.T) {
	logger := log.NewNopLogger()

	body := `{
		"status": "success",
		"data": {
			"resultType": "vector",
			"result": null,
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

	data, ok := response["data"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "vector", data["resultType"])

	result, ok := data["result"].([]any)
	require.True(t, ok)
	require.Empty(t, result)
}

func TestHandleLokiQueries_NullScalarResult_IsSkipped(t *testing.T) {
	logger := log.NewNopLogger()

	body := `{
		"status": "success",
		"data": {
			"resultType": "scalar",
			"result": null,
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
	require.Equal(t, http.StatusBadGateway, w.Code)
}

func TestHandleLokiQueries_NullUnknownResultType_IsSkipped(t *testing.T) {
	logger := log.NewNopLogger()

	body := `{
		"status": "success",
		"data": {
			"resultType": "unknown",
			"result": null,
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
	require.Equal(t, http.StatusBadGateway, w.Code)
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

func TestHandleLokiQueries_MismatchedResultTypes_SkipsMismatchingBackend(t *testing.T) {
	logger := log.NewNopLogger()

	streams := `{
		"status": "success",
		"data": {
			"resultType": "streams",
			"result": [{"stream": {"app": "nginx"}, "values": [["1609459200000000000", "log 1"]]}],
			"stats": {}
		}
	}`
	matrix := `{
		"status": "success",
		"data": {
			"resultType": "matrix",
			"result": [{"metric": {"__name__": "x"}, "values": [[1609459200, "1"]]}],
			"stats": {}
		}
	}`

	results := make(chan *proxyresponse.BackendResponse, 2)
	{
		rec := httptest.NewRecorder()
		rec.WriteString(streams)
		results <- wrapResponse(rec.Result())
	}
	{
		rec := httptest.NewRecorder()
		rec.WriteString(matrix)
		results <- wrapResponse(rec.Result())
	}
	close(results)

	w := httptest.NewRecorder()
	HandleLokiQueries(t.Context(), w, results, logger)

	var response map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

	data, ok := response["data"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "streams", data["resultType"])

	result, ok := data["result"].([]any)
	require.True(t, ok)
	require.Len(t, result, 1)
}

func TestHandleLokiQueries_UnknownResultType_ReturnsBadGateway(t *testing.T) {
	logger := log.NewNopLogger()

	body := `{
		"status": "success",
		"data": {
			"resultType": "unknown",
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

	require.Equal(t, http.StatusBadGateway, w.Code)
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
						["1609459200000000000", "log line", {"structuredMetadata": {"key": "value"}}]
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

func TestHandleLokiQueries_WithCategorizedMetadataArrays(t *testing.T) {
	logger := log.NewNopLogger()

	body := `{
		"status": "success",
		"data": {
			"resultType": "streams",
			"result": [
				{
					"stream": {"app": "nginx"},
					"values": [
						["1609459200000000000", "{\"message\":\"Update event got: PlacesData 3415\"}", {"structuredMetadata": {"trace_id": "0242ac120002"}, "parsed": {"level": "info"}}],
						["1609459201000000000", "{\"message\":\"next log line\"}"]
					]
				}
			],
			"stats": {},
			"encodingFlags": ["categorize-labels"]
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

	stream, ok := result[0].(map[string]any)
	require.True(t, ok)

	values, ok := stream["values"].([]any)
	require.True(t, ok)
	require.Len(t, values, 2)

	firstValue, ok := values[0].([]any)
	require.True(t, ok)
	require.Len(t, firstValue, 3)

	metadata, ok := firstValue[2].(map[string]any)
	require.True(t, ok)
	_, hasStructuredMetadata := metadata["structuredMetadata"].(map[string]any)
	_, hasParsed := metadata["parsed"].(map[string]any)
	require.True(t, hasStructuredMetadata)
	require.True(t, hasParsed)
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

	require.Equal(t, http.StatusBadGateway, w.Code)

	var response map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

	require.Equal(t, "error", response["status"])
	require.Equal(t, "backend_error", response["errorType"])
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

	require.Equal(t, http.StatusBadGateway, w.Code)

	var response map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

	require.Equal(t, "error", response["status"])
	require.Equal(t, "backend_error", response["errorType"])
}

func TestHandleLokiQueries_ResponseReaderCanceled_IsIgnoredWhenOthersSucceed(t *testing.T) {
	logger := log.NewNopLogger()

	results := make(chan *proxyresponse.BackendResponse, 2)
	results <- wrapResponse(&http.Response{
		StatusCode: 200,
		Body:       &canceledQueryReader{},
	})

	rec := httptest.NewRecorder()
	rec.WriteString(`{
		"status": "success",
		"data": {
			"resultType": "streams",
			"result": [{"stream": {"app": "api"}, "values": []}],
			"stats": {}
		}
	}`)
	results <- wrapResponse(rec.Result())
	close(results)

	w := httptest.NewRecorder()
	HandleLokiQueries(t.Context(), w, results, logger)

	var response map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

	data, ok := response["data"].(map[string]any)
	require.True(t, ok)

	result, ok := data["result"].([]any)
	require.True(t, ok)
	require.Len(t, result, 1)
}

func TestHandleLokiQueries_ResponseReaderDeadlineExceeded_IsIgnoredWhenOthersSucceed(t *testing.T) {
	logger := log.NewNopLogger()

	results := make(chan *proxyresponse.BackendResponse, 2)
	results <- wrapResponse(&http.Response{
		StatusCode: 200,
		Body:       &deadlineQueryReader{},
	})

	rec := httptest.NewRecorder()
	rec.WriteString(`{
		"status": "success",
		"data": {
			"resultType": "streams",
			"result": [{"stream": {"app": "api"}, "values": []}],
			"stats": {}
		}
	}`)
	results <- wrapResponse(rec.Result())
	close(results)

	w := httptest.NewRecorder()
	HandleLokiQueries(t.Context(), w, results, logger)

	var response map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

	data, ok := response["data"].(map[string]any)
	require.True(t, ok)

	result, ok := data["result"].([]any)
	require.True(t, ok)
	require.Len(t, result, 1)
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

	require.Equal(t, http.StatusBadGateway, w.Code)

	var response map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

	require.Equal(t, "error", response["status"])
	require.Equal(t, "backend_error", response["errorType"])
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

// failingQueryReader always fails on Read (simulates network/IO failure)
type failingQueryReader struct{}

func (f *failingQueryReader) Read([]byte) (int, error) {
	return 0, errors.New("read error")
}

func (f *failingQueryReader) Close() error {
	return nil
}

// canceledQueryReader always fails with context cancellation.
type canceledQueryReader struct{}

func (c *canceledQueryReader) Read([]byte) (int, error) {
	return 0, context.Canceled
}

func (c *canceledQueryReader) Close() error {
	return nil
}

// deadlineQueryReader always fails with context deadline exceeded.
type deadlineQueryReader struct{}

func (d *deadlineQueryReader) Read([]byte) (int, error) {
	return 0, context.DeadlineExceeded
}

func (d *deadlineQueryReader) Close() error {
	return nil
}
