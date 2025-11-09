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

func TestHandleLokiStats_SingleResponse(t *testing.T) {
	logger := log.NewNopLogger()

	body := `{
		"streams": 10,
		"chunks": 100,
		"bytes": 1024000,
		"entries": 50000
	}`

	results := make(chan *http.Response, 1)
	rec := httptest.NewRecorder()
	rec.WriteString(body)
	results <- rec.Result()
	close(results)

	w := httptest.NewRecorder()
	HandleLokiStats(t.Context(), w, results, logger)

	var response map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

	require.Equal(t, float64(10), response["streams"])
	require.Equal(t, float64(100), response["chunks"])
	require.Equal(t, float64(1024000), response["bytes"])
	require.Equal(t, float64(50000), response["entries"])
}

func TestHandleLokiStats_MultipleResponses(t *testing.T) {
	logger := log.NewNopLogger()

	responses := []string{
		`{"streams": 10, "chunks": 100, "bytes": 1000, "entries": 500}`,
		`{"streams": 20, "chunks": 200, "bytes": 2000, "entries": 1000}`,
		`{"streams": 30, "chunks": 300, "bytes": 3000, "entries": 1500}`,
	}

	results := make(chan *http.Response, len(responses))
	for _, respBody := range responses {
		rec := httptest.NewRecorder()
		rec.WriteString(respBody)
		results <- rec.Result()
	}
	close(results)

	w := httptest.NewRecorder()
	HandleLokiStats(t.Context(), w, results, logger)

	var response map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

	// Values should be summed across all backends
	require.Equal(t, float64(60), response["streams"])  // 10 + 20 + 30
	require.Equal(t, float64(600), response["chunks"])  // 100 + 200 + 300
	require.Equal(t, float64(6000), response["bytes"])  // 1000 + 2000 + 3000
	require.Equal(t, float64(3000), response["entries"]) // 500 + 1000 + 1500
}

func TestHandleLokiStats_EmptyStats(t *testing.T) {
	logger := log.NewNopLogger()

	body := `{"streams": 0, "chunks": 0, "bytes": 0, "entries": 0}`

	results := make(chan *http.Response, 1)
	rec := httptest.NewRecorder()
	rec.WriteString(body)
	results <- rec.Result()
	close(results)

	w := httptest.NewRecorder()
	HandleLokiStats(t.Context(), w, results, logger)

	var response map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

	require.Equal(t, float64(0), response["streams"])
	require.Equal(t, float64(0), response["chunks"])
	require.Equal(t, float64(0), response["bytes"])
	require.Equal(t, float64(0), response["entries"])
}

func TestHandleLokiStats_InvalidJSON(t *testing.T) {
	logger := log.NewNopLogger()

	results := make(chan *http.Response, 1)
	rec := httptest.NewRecorder()
	rec.WriteString("invalid json")
	results <- rec.Result()
	close(results)

	w := httptest.NewRecorder()
	HandleLokiStats(t.Context(), w, results, logger)

	var response map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

	// Should return zero values on unmarshal error
	require.Equal(t, float64(0), response["streams"])
	require.Equal(t, float64(0), response["chunks"])
	require.Equal(t, float64(0), response["bytes"])
	require.Equal(t, float64(0), response["entries"])
}

func TestHandleLokiStats_ResponseReaderError(t *testing.T) {
	logger := log.NewNopLogger()

	results := make(chan *http.Response, 1)
	results <- &http.Response{
		StatusCode: 200,
		Body:       &failingStatsReader{},
	}
	close(results)

	w := httptest.NewRecorder()
	HandleLokiStats(t.Context(), w, results, logger)

	var response map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

	// Should return zero values on read error
	require.Equal(t, float64(0), response["streams"])
	require.Equal(t, float64(0), response["chunks"])
	require.Equal(t, float64(0), response["bytes"])
	require.Equal(t, float64(0), response["entries"])
}

func TestHandleLokiStats_PartialFailure(t *testing.T) {
	logger := log.NewNopLogger()

	results := make(chan *http.Response, 3)

	// Valid response
	rec1 := httptest.NewRecorder()
	rec1.WriteString(`{"streams": 10, "chunks": 100, "bytes": 1000, "entries": 500}`)
	results <- rec1.Result()

	// Invalid JSON
	rec2 := httptest.NewRecorder()
	rec2.WriteString("invalid json")
	results <- rec2.Result()

	// Valid response
	rec3 := httptest.NewRecorder()
	rec3.WriteString(`{"streams": 20, "chunks": 200, "bytes": 2000, "entries": 1000}`)
	results <- rec3.Result()

	close(results)

	w := httptest.NewRecorder()
	HandleLokiStats(t.Context(), w, results, logger)

	var response map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

	// Should sum only the successful responses
	require.Equal(t, float64(30), response["streams"])   // 10 + 20
	require.Equal(t, float64(300), response["chunks"])   // 100 + 200
	require.Equal(t, float64(3000), response["bytes"])   // 1000 + 2000
	require.Equal(t, float64(1500), response["entries"]) // 500 + 1000
}

func TestHandleLokiStats_LargeNumbers(t *testing.T) {
	logger := log.NewNopLogger()

	body := `{
		"streams": 1000000,
		"chunks": 50000000,
		"bytes": 10737418240,
		"entries": 500000000
	}`

	results := make(chan *http.Response, 1)
	rec := httptest.NewRecorder()
	rec.WriteString(body)
	results <- rec.Result()
	close(results)

	w := httptest.NewRecorder()
	HandleLokiStats(t.Context(), w, results, logger)

	var response map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

	require.Equal(t, float64(1000000), response["streams"])
	require.Equal(t, float64(50000000), response["chunks"])
	require.Equal(t, float64(10737418240), response["bytes"])
	require.Equal(t, float64(500000000), response["entries"])
}

func TestHandleLokiStats_MixedZeroAndNonZero(t *testing.T) {
	logger := log.NewNopLogger()

	responses := []string{
		`{"streams": 0, "chunks": 0, "bytes": 0, "entries": 0}`,
		`{"streams": 10, "chunks": 100, "bytes": 1000, "entries": 500}`,
		`{"streams": 0, "chunks": 0, "bytes": 0, "entries": 0}`,
	}

	results := make(chan *http.Response, len(responses))
	for _, respBody := range responses {
		rec := httptest.NewRecorder()
		rec.WriteString(respBody)
		results <- rec.Result()
	}
	close(results)

	w := httptest.NewRecorder()
	HandleLokiStats(t.Context(), w, results, logger)

	var response map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

	// Only the middle backend has values
	require.Equal(t, float64(10), response["streams"])
	require.Equal(t, float64(100), response["chunks"])
	require.Equal(t, float64(1000), response["bytes"])
	require.Equal(t, float64(500), response["entries"])
}

func TestHandleLokiStats_NoResponses(t *testing.T) {
	logger := log.NewNopLogger()

	results := make(chan *http.Response)
	close(results)

	w := httptest.NewRecorder()
	HandleLokiStats(t.Context(), w, results, logger)

	var response map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

	// Should return zero values when no responses
	require.Equal(t, float64(0), response["streams"])
	require.Equal(t, float64(0), response["chunks"])
	require.Equal(t, float64(0), response["bytes"])
	require.Equal(t, float64(0), response["entries"])
}

// failingStatsReader always fails on Read (simulates network/IO failure)
type failingStatsReader struct{}

func (f *failingStatsReader) Read([]byte) (int, error) {
	return 0, errors.New("read error")
}

func (f *failingStatsReader) Close() error {
	return nil
}
