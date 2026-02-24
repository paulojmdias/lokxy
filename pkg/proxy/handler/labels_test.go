package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-kit/log"
	"github.com/paulojmdias/lokxy/pkg/proxy/proxyresponse"
	"github.com/stretchr/testify/require"
)

func TestHandleLokiLabels_SingleResponse(t *testing.T) {
	logger := log.NewNopLogger()

	body := `{
		"status": "success",
		"data": ["app", "environment", "instance", "job"]
	}`

	results := make(chan *proxyresponse.BackendResponse, 1)
	rec := httptest.NewRecorder()
	rec.WriteString(body)
	results <- wrapResponse(rec.Result())
	close(results)

	w := httptest.NewRecorder()
	HandleLokiLabels(t.Context(), w, results, logger)

	var response map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

	require.Equal(t, "success", response["status"])
	data, ok := response["data"].([]any)
	require.True(t, ok)
	require.Len(t, data, 4)
}

func TestHandleLokiLabels_MultipleResponses(t *testing.T) {
	logger := log.NewNopLogger()

	responses := []string{
		`{"status": "success", "data": ["app", "environment", "instance"]}`,
		`{"status": "success", "data": ["job", "region", "app"]}`,
		`{"status": "success", "data": ["cluster", "environment"]}`,
	}

	results := make(chan *proxyresponse.BackendResponse, len(responses))
	for _, respBody := range responses {
		rec := httptest.NewRecorder()
		rec.WriteString(respBody)
		results <- wrapResponse(rec.Result())
	}
	close(results)

	w := httptest.NewRecorder()
	HandleLokiLabels(t.Context(), w, results, logger)

	var response map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

	require.Equal(t, "success", response["status"])
	data, ok := response["data"].([]any)
	require.True(t, ok)

	// Should have unique labels from all responses
	// app, environment, instance, job, region, cluster = 6 unique labels
	require.Len(t, data, 6)

	// Verify labels are sorted
	labels := make([]string, len(data))
	for i, v := range data {
		labels[i] = v.(string)
	}
	for i := 1; i < len(labels); i++ {
		require.Less(t, labels[i-1], labels[i], "labels should be sorted")
	}
}

func TestHandleLokiLabels_EmptyResponse(t *testing.T) {
	logger := log.NewNopLogger()

	body := `{"status": "success", "data": []}`

	results := make(chan *proxyresponse.BackendResponse, 1)
	rec := httptest.NewRecorder()
	rec.WriteString(body)
	results <- wrapResponse(rec.Result())
	close(results)

	w := httptest.NewRecorder()
	HandleLokiLabels(t.Context(), w, results, logger)

	var response map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

	require.Equal(t, "success", response["status"])
	data, ok := response["data"].([]any)
	require.True(t, ok)
	require.Empty(t, data)
}

func TestHandleLokiLabels_InvalidJSON(t *testing.T) {
	logger := log.NewNopLogger()

	results := make(chan *proxyresponse.BackendResponse, 1)
	rec := httptest.NewRecorder()
	rec.WriteString("invalid json")
	results <- wrapResponse(rec.Result())
	close(results)

	w := httptest.NewRecorder()
	HandleLokiLabels(t.Context(), w, results, logger)

	var response map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

	// Should return empty success response on unmarshal error
	require.Equal(t, "success", response["status"])
	data, ok := response["data"].([]any)
	require.True(t, ok)
	require.Empty(t, data)
}

func TestHandleLokiLabels_ResponseReaderError(t *testing.T) {
	logger := log.NewNopLogger()

	results := make(chan *proxyresponse.BackendResponse, 1)
	results <- wrapResponse(&http.Response{
		StatusCode: 200,
		Body:       &failingLabelsReader{},
	})
	close(results)

	w := httptest.NewRecorder()
	HandleLokiLabels(t.Context(), w, results, logger)

	var response map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

	// Should return empty success response on read error
	require.Equal(t, "success", response["status"])
	data, ok := response["data"].([]any)
	require.True(t, ok)
	require.Empty(t, data)
}

func TestHandleLokiLabels_DuplicateLabelsAcrossBackends(t *testing.T) {
	logger := log.NewNopLogger()

	// All backends return same labels - should deduplicate
	responses := []string{
		`{"status": "success", "data": ["app", "job"]}`,
		`{"status": "success", "data": ["app", "job"]}`,
		`{"status": "success", "data": ["app", "job"]}`,
	}

	results := make(chan *proxyresponse.BackendResponse, len(responses))
	for _, respBody := range responses {
		rec := httptest.NewRecorder()
		rec.WriteString(respBody)
		results <- wrapResponse(rec.Result())
	}
	close(results)

	w := httptest.NewRecorder()
	HandleLokiLabels(t.Context(), w, results, logger)

	var response map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

	require.Equal(t, "success", response["status"])
	data, ok := response["data"].([]any)
	require.True(t, ok)

	// Should only have 2 unique labels despite 3 backends
	require.Len(t, data, 2)
	require.Contains(t, data, "app")
	require.Contains(t, data, "job")
}

func TestHandleLokiLabels_PartialFailure(t *testing.T) {
	logger := log.NewNopLogger()

	results := make(chan *proxyresponse.BackendResponse, 3)

	// Valid response
	rec1 := httptest.NewRecorder()
	rec1.WriteString(`{"status": "success", "data": ["app", "job"]}`)
	results <- wrapResponse(rec1.Result())

	// Invalid JSON
	rec2 := httptest.NewRecorder()
	rec2.WriteString("invalid json")
	results <- wrapResponse(rec2.Result())

	// Valid response
	rec3 := httptest.NewRecorder()
	rec3.WriteString(`{"status": "success", "data": ["region", "cluster"]}`)
	results <- wrapResponse(rec3.Result())

	close(results)

	w := httptest.NewRecorder()
	HandleLokiLabels(t.Context(), w, results, logger)

	var response map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

	require.Equal(t, "success", response["status"])
	data, ok := response["data"].([]any)
	require.True(t, ok)

	// Should have labels from 2 successful responses
	require.Len(t, data, 4) // app, job, region, cluster
}

// failingLabelsReader always fails on Read (simulates network/IO failure)
type failingLabelsReader struct{}

func (f *failingLabelsReader) Read([]byte) (int, error) {
	return 0, errors.New("read error")
}

func (f *failingLabelsReader) Close() error {
	return nil
}
