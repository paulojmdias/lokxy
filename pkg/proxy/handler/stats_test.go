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

type statsResponse struct {
	Streams int `json:"streams"`
	Chunks  int `json:"chunks"`
	Bytes   int `json:"bytes"`
	Entries int `json:"entries"`
}

func decodeStatsResponse(t *testing.T, w *httptest.ResponseRecorder) statsResponse {
	t.Helper()

	var response statsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

	return response
}

func TestHandleLokiStats_SingleResponse(t *testing.T) {
	logger := log.NewNopLogger()

	body := `{
		"streams": 10,
		"chunks": 100,
		"bytes": 1024000,
		"entries": 50000
	}`

	results := make(chan *proxyresponse.BackendResponse, 1)
	rec := httptest.NewRecorder()
	rec.WriteString(body)
	results <- wrapResponse(rec.Result())
	close(results)

	w := httptest.NewRecorder()
	HandleLokiStats(t.Context(), w, results, logger)

	response := decodeStatsResponse(t, w)

	require.Equal(t, 10, response.Streams)
	require.Equal(t, 100, response.Chunks)
	require.Equal(t, 1024000, response.Bytes)
	require.Equal(t, 50000, response.Entries)
}

func TestHandleLokiStats_MultipleResponses(t *testing.T) {
	logger := log.NewNopLogger()

	responses := []string{
		`{"streams": 10, "chunks": 100, "bytes": 1000, "entries": 500}`,
		`{"streams": 20, "chunks": 200, "bytes": 2000, "entries": 1000}`,
		`{"streams": 30, "chunks": 300, "bytes": 3000, "entries": 1500}`,
	}

	results := make(chan *proxyresponse.BackendResponse, len(responses))
	for _, respBody := range responses {
		rec := httptest.NewRecorder()
		rec.WriteString(respBody)
		results <- wrapResponse(rec.Result())
	}
	close(results)

	w := httptest.NewRecorder()
	HandleLokiStats(t.Context(), w, results, logger)

	response := decodeStatsResponse(t, w)

	// Values should be summed across all backends
	require.Equal(t, 60, response.Streams)   // 10 + 20 + 30
	require.Equal(t, 600, response.Chunks)   // 100 + 200 + 300
	require.Equal(t, 6000, response.Bytes)   // 1000 + 2000 + 3000
	require.Equal(t, 3000, response.Entries) // 500 + 1000 + 1500
}

func TestHandleLokiStats_EmptyStats(t *testing.T) {
	logger := log.NewNopLogger()

	body := `{"streams": 0, "chunks": 0, "bytes": 0, "entries": 0}`

	results := make(chan *proxyresponse.BackendResponse, 1)
	rec := httptest.NewRecorder()
	rec.WriteString(body)
	results <- wrapResponse(rec.Result())
	close(results)

	w := httptest.NewRecorder()
	HandleLokiStats(t.Context(), w, results, logger)

	response := decodeStatsResponse(t, w)

	require.Equal(t, 0, response.Streams)
	require.Equal(t, 0, response.Chunks)
	require.Equal(t, 0, response.Bytes)
	require.Equal(t, 0, response.Entries)
}

func TestHandleLokiStats_InvalidJSON(t *testing.T) {
	logger := log.NewNopLogger()

	results := make(chan *proxyresponse.BackendResponse, 1)
	rec := httptest.NewRecorder()
	rec.WriteString("invalid json")
	results <- wrapResponse(rec.Result())
	close(results)

	w := httptest.NewRecorder()
	HandleLokiStats(t.Context(), w, results, logger)

	response := decodeStatsResponse(t, w)

	// Should return zero values on unmarshal error
	require.Equal(t, 0, response.Streams)
	require.Equal(t, 0, response.Chunks)
	require.Equal(t, 0, response.Bytes)
	require.Equal(t, 0, response.Entries)
}

func TestHandleLokiStats_ResponseReaderError(t *testing.T) {
	logger := log.NewNopLogger()

	results := make(chan *proxyresponse.BackendResponse, 1)
	results <- wrapResponse(&http.Response{
		StatusCode: 200,
		Body:       &failingStatsReader{},
	})
	close(results)

	w := httptest.NewRecorder()
	HandleLokiStats(t.Context(), w, results, logger)

	response := decodeStatsResponse(t, w)

	// Should return zero values on read error
	require.Equal(t, 0, response.Streams)
	require.Equal(t, 0, response.Chunks)
	require.Equal(t, 0, response.Bytes)
	require.Equal(t, 0, response.Entries)
}

func TestHandleLokiStats_PartialFailure(t *testing.T) {
	logger := log.NewNopLogger()

	results := make(chan *proxyresponse.BackendResponse, 3)

	// Valid response
	rec1 := httptest.NewRecorder()
	rec1.WriteString(`{"streams": 10, "chunks": 100, "bytes": 1000, "entries": 500}`)
	results <- wrapResponse(rec1.Result())

	// Invalid JSON
	rec2 := httptest.NewRecorder()
	rec2.WriteString("invalid json")
	results <- wrapResponse(rec2.Result())

	// Valid response
	rec3 := httptest.NewRecorder()
	rec3.WriteString(`{"streams": 20, "chunks": 200, "bytes": 2000, "entries": 1000}`)
	results <- wrapResponse(rec3.Result())

	close(results)

	w := httptest.NewRecorder()
	HandleLokiStats(t.Context(), w, results, logger)

	response := decodeStatsResponse(t, w)

	// Should sum only the successful responses
	require.Equal(t, 30, response.Streams)   // 10 + 20
	require.Equal(t, 300, response.Chunks)   // 100 + 200
	require.Equal(t, 3000, response.Bytes)   // 1000 + 2000
	require.Equal(t, 1500, response.Entries) // 500 + 1000
}

func TestHandleLokiStats_LargeNumbers(t *testing.T) {
	logger := log.NewNopLogger()

	body := `{
		"streams": 1000000,
		"chunks": 50000000,
		"bytes": 10737418240,
		"entries": 500000000
	}`

	results := make(chan *proxyresponse.BackendResponse, 1)
	rec := httptest.NewRecorder()
	rec.WriteString(body)
	results <- wrapResponse(rec.Result())
	close(results)

	w := httptest.NewRecorder()
	HandleLokiStats(t.Context(), w, results, logger)

	response := decodeStatsResponse(t, w)

	require.Equal(t, 1000000, response.Streams)
	require.Equal(t, 50000000, response.Chunks)
	require.Equal(t, 10737418240, response.Bytes)
	require.Equal(t, 500000000, response.Entries)
}

func TestHandleLokiStats_MixedZeroAndNonZero(t *testing.T) {
	logger := log.NewNopLogger()

	responses := []string{
		`{"streams": 0, "chunks": 0, "bytes": 0, "entries": 0}`,
		`{"streams": 10, "chunks": 100, "bytes": 1000, "entries": 500}`,
		`{"streams": 0, "chunks": 0, "bytes": 0, "entries": 0}`,
	}

	results := make(chan *proxyresponse.BackendResponse, len(responses))
	for _, respBody := range responses {
		rec := httptest.NewRecorder()
		rec.WriteString(respBody)
		results <- wrapResponse(rec.Result())
	}
	close(results)

	w := httptest.NewRecorder()
	HandleLokiStats(t.Context(), w, results, logger)

	response := decodeStatsResponse(t, w)

	// Only the middle backend has values
	require.Equal(t, 10, response.Streams)
	require.Equal(t, 100, response.Chunks)
	require.Equal(t, 1000, response.Bytes)
	require.Equal(t, 500, response.Entries)
}

func TestHandleLokiStats_NoResponses(t *testing.T) {
	logger := log.NewNopLogger()

	results := make(chan *proxyresponse.BackendResponse)
	close(results)

	w := httptest.NewRecorder()
	HandleLokiStats(t.Context(), w, results, logger)

	response := decodeStatsResponse(t, w)

	// Should return zero values when no responses
	require.Equal(t, 0, response.Streams)
	require.Equal(t, 0, response.Chunks)
	require.Equal(t, 0, response.Bytes)
	require.Equal(t, 0, response.Entries)
}

// failingStatsReader always fails on Read (simulates network/IO failure)
type failingStatsReader struct{}

func (f *failingStatsReader) Read([]byte) (int, error) {
	return 0, errors.New("read error")
}

func (f *failingStatsReader) Close() error {
	return nil
}
