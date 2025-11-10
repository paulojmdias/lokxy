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

func TestHandleLokiSeries_SingleResponse(t *testing.T) {
	logger := log.NewNopLogger()

	body := `{
		"status": "success",
		"data": [
			{"app": "nginx", "environment": "prod"},
			{"app": "api", "environment": "staging"}
		]
	}`

	results := make(chan *http.Response, 1)
	rec := httptest.NewRecorder()
	rec.WriteString(body)
	results <- rec.Result()
	close(results)

	w := httptest.NewRecorder()
	HandleLokiSeries(t.Context(), w, results, logger)

	var response map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

	require.Equal(t, "success", response["status"])
	data, ok := response["data"].([]any)
	require.True(t, ok)
	require.Len(t, data, 2)
}

func TestHandleLokiSeries_MultipleResponses(t *testing.T) {
	logger := log.NewNopLogger()

	responses := []string{
		`{
			"status": "success",
			"data": [
				{"app": "nginx", "environment": "prod"},
				{"app": "api", "environment": "prod"}
			]
		}`,
		`{
			"status": "success",
			"data": [
				{"app": "frontend", "environment": "staging"},
				{"app": "backend", "environment": "staging"}
			]
		}`,
		`{
			"status": "success",
			"data": [
				{"app": "worker", "environment": "dev"}
			]
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
	HandleLokiSeries(t.Context(), w, results, logger)

	var response map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

	require.Equal(t, "success", response["status"])
	data, ok := response["data"].([]any)
	require.True(t, ok)

	// Should have all 5 series from all backends
	require.Len(t, data, 5)
}

func TestHandleLokiSeries_EmptyResponse(t *testing.T) {
	logger := log.NewNopLogger()

	body := `{"status": "success", "data": []}`

	results := make(chan *http.Response, 1)
	rec := httptest.NewRecorder()
	rec.WriteString(body)
	results <- rec.Result()
	close(results)

	w := httptest.NewRecorder()
	HandleLokiSeries(t.Context(), w, results, logger)

	var response map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

	require.Equal(t, "success", response["status"])
	// Data can be null or empty array
	if data, ok := response["data"].([]any); ok {
		require.Empty(t, data)
	}
}

func TestHandleLokiSeries_InvalidJSON(t *testing.T) {
	logger := log.NewNopLogger()

	results := make(chan *http.Response, 1)
	rec := httptest.NewRecorder()
	rec.WriteString("invalid json")
	results <- rec.Result()
	close(results)

	w := httptest.NewRecorder()
	HandleLokiSeries(t.Context(), w, results, logger)

	var response map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

	// Should return empty success response on unmarshal error
	require.Equal(t, "success", response["status"])
	// Data can be null or empty array
	if data, ok := response["data"].([]any); ok {
		require.Empty(t, data)
	}
}

func TestHandleLokiSeries_ResponseReaderError(t *testing.T) {
	logger := log.NewNopLogger()

	results := make(chan *http.Response, 1)
	results <- &http.Response{
		StatusCode: 200,
		Body:       &failingSeriesReader{},
	}
	close(results)

	w := httptest.NewRecorder()
	HandleLokiSeries(t.Context(), w, results, logger)

	var response map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

	// Should return empty success response on read error
	require.Equal(t, "success", response["status"])
	// Data can be null or empty array
	if data, ok := response["data"].([]any); ok {
		require.Empty(t, data)
	}
}

func TestHandleLokiSeries_PartialFailure(t *testing.T) {
	logger := log.NewNopLogger()

	results := make(chan *http.Response, 3)

	// Valid response
	rec1 := httptest.NewRecorder()
	rec1.WriteString(`{"status": "success", "data": [{"app": "nginx"}]}`)
	results <- rec1.Result()

	// Invalid JSON
	rec2 := httptest.NewRecorder()
	rec2.WriteString("invalid json")
	results <- rec2.Result()

	// Valid response
	rec3 := httptest.NewRecorder()
	rec3.WriteString(`{"status": "success", "data": [{"app": "api"}, {"app": "worker"}]}`)
	results <- rec3.Result()

	close(results)

	w := httptest.NewRecorder()
	HandleLokiSeries(t.Context(), w, results, logger)

	var response map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

	require.Equal(t, "success", response["status"])
	data, ok := response["data"].([]any)
	require.True(t, ok)

	// Should have series from 2 successful responses
	require.Len(t, data, 3)
}

func TestHandleLokiSeries_DuplicateSeriesAcrossBackends(t *testing.T) {
	logger := log.NewNopLogger()

	// Multiple backends can return duplicate series - should keep all
	responses := []string{
		`{"status": "success", "data": [{"app": "nginx", "env": "prod"}]}`,
		`{"status": "success", "data": [{"app": "nginx", "env": "prod"}]}`,
	}

	results := make(chan *http.Response, len(responses))
	for _, respBody := range responses {
		rec := httptest.NewRecorder()
		rec.WriteString(respBody)
		results <- rec.Result()
	}
	close(results)

	w := httptest.NewRecorder()
	HandleLokiSeries(t.Context(), w, results, logger)

	var response map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

	require.Equal(t, "success", response["status"])
	data, ok := response["data"].([]any)
	require.True(t, ok)

	// Current implementation appends all series (no deduplication)
	// This matches Loki's behavior where series are aggregated
	require.Len(t, data, 2)
}

func TestHandleLokiSeries_ComplexLabels(t *testing.T) {
	logger := log.NewNopLogger()

	body := `{
		"status": "success",
		"data": [
			{
				"__name__": "logs",
				"app": "nginx",
				"environment": "production",
				"region": "us-west-2",
				"instance": "i-1234567890"
			},
			{
				"__name__": "logs",
				"app": "api",
				"environment": "production",
				"region": "us-east-1",
				"instance": "i-0987654321"
			}
		]
	}`

	results := make(chan *http.Response, 1)
	rec := httptest.NewRecorder()
	rec.WriteString(body)
	results <- rec.Result()
	close(results)

	w := httptest.NewRecorder()
	HandleLokiSeries(t.Context(), w, results, logger)

	var response map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

	require.Equal(t, "success", response["status"])
	data, ok := response["data"].([]any)
	require.True(t, ok)
	require.Len(t, data, 2)

	// Verify complex labels are preserved
	series0, ok := data[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "nginx", series0["app"])
	require.Equal(t, "production", series0["environment"])
	require.Equal(t, "us-west-2", series0["region"])
}

// failingSeriesReader always fails on Read (simulates network/IO failure)
type failingSeriesReader struct{}

func (f *failingSeriesReader) Read([]byte) (int, error) {
	return 0, errors.New("read error")
}

func (f *failingSeriesReader) Close() error {
	return nil
}
