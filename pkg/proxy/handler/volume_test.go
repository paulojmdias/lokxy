package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-kit/log"
	"github.com/stretchr/testify/require"

	"github.com/paulojmdias/lokxy/pkg/proxy/proxyresponse"
)

func TestHandleLokiVolume(t *testing.T) {
	logger := log.NewNopLogger()

	tests := []struct {
		name            string
		responses       []string
		expectedVolumes int
		expectedStatus  string
	}{
		{
			name: "single response",
			responses: []string{`{
				"status": "success",
				"data": {
					"resultType": "vector",
					"result": [
						{
							"metric": {"__name__": "volume_bytes"},
							"value": ["1609459200", "1024"]
						}
					]
				}
			}`},
			expectedVolumes: 1,
			expectedStatus:  statusSuccess,
		},
		{
			name: "multiple responses with different metrics",
			responses: []string{
				`{
					"status": "success",
					"data": {
						"resultType": "vector",
						"result": [
							{
								"metric": {"__name__": "volume_bytes", "instance": "loki1"},
								"value": ["1609459200", "1024"]
							}
						]
					}
				}`,
				`{
					"status": "success",
					"data": {
						"resultType": "vector",
						"result": [
							{
								"metric": {"__name__": "volume_bytes", "instance": "loki2"},
								"value": ["1609459200", "2048"]
							}
						]
					}
				}`,
			},
			expectedVolumes: 2,
			expectedStatus:  statusSuccess,
		},
		{
			name: "empty response",
			responses: []string{`{
				"status": "success",
				"data": {
					"resultType": "vector",
					"result": []
				}
			}`},
			expectedVolumes: 0,
			expectedStatus:  statusSuccess,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results := make(chan *proxyresponse.BackendResponse, len(tt.responses))

			for _, respBody := range tt.responses {
				resp := httptest.NewRecorder()
				resp.WriteString(respBody)
				results <- wrapResponse(resp.Result())
			}
			close(results)

			w := httptest.NewRecorder()
			HandleLokiVolume(t.Context(), w, results, logger)

			var volumeResponse VolumeResponse
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &volumeResponse))

			require.Equal(t, tt.expectedStatus, volumeResponse.Status)
			require.Len(t, volumeResponse.Data.Result, tt.expectedVolumes)
		})
	}
}

func TestHandleLokiVolumeWithInvalidJSON(t *testing.T) {
	logger := log.NewNopLogger()

	results := make(chan *proxyresponse.BackendResponse, 1)
	resp := httptest.NewRecorder()
	resp.WriteString("invalid json")
	results <- wrapResponse(resp.Result())
	close(results)

	w := httptest.NewRecorder()
	HandleLokiVolume(t.Context(), w, results, logger)

	var volumeResponse VolumeResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &volumeResponse))

	require.Equal(t, statusSuccess, volumeResponse.Status)
	require.Empty(t, volumeResponse.Data.Result)
}

func TestHandleLokiVolumeResponseReaderError(t *testing.T) {
	logger := log.NewNopLogger()

	results := make(chan *proxyresponse.BackendResponse, 1)
	resp := &http.Response{
		StatusCode: 200,
		Body:       &failingReader{},
	}
	results <- wrapResponse(resp)
	close(results)

	w := httptest.NewRecorder()
	HandleLokiVolume(t.Context(), w, results, logger)

	var volumeResponse VolumeResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &volumeResponse))

	require.Equal(t, statusSuccess, volumeResponse.Status)
	require.Empty(t, volumeResponse.Data.Result)
}

// failingReader is a helper that always returns an error
type failingReader struct{}

func (f *failingReader) Read([]byte) (int, error) {
	return 0, errors.New("read error")
}
func (f *failingReader) Close() error { return nil }

func TestParseVolumeValue(t *testing.T) {
	tests := []struct {
		name     string
		input    any
		expected int64
	}{
		{"string valid", "1024", int64(1024)},
		{"string invalid", "abc", int64(0)},
		{"float64", float64(2048.9), int64(2048)},
		{"int64", int64(512), int64(512)},
		{"int", int(256), int64(256)},
		{"nil unknown type", nil, int64(0)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, parseVolumeValue(tt.input))
		})
	}
}

func TestMergeMatrixValues(t *testing.T) {
	tests := []struct {
		name     string
		existing [][]any
		newVals  [][]any
		// only check length and timestamps are sorted; exact values checked per case
		wantLen int
	}{
		{
			name:     "both empty",
			existing: nil,
			newVals:  nil,
			wantLen:  0,
		},
		{
			name:     "existing empty returns new",
			existing: nil,
			newVals:  [][]any{{float64(1000), "10"}},
			wantLen:  1,
		},
		{
			name:     "new empty returns existing",
			existing: [][]any{{float64(1000), "10"}},
			newVals:  nil,
			wantLen:  1,
		},
		{
			name:     "non-overlapping timestamps union",
			existing: [][]any{{float64(1000), "10"}},
			newVals:  [][]any{{float64(2000), "20"}},
			wantLen:  2,
		},
		{
			name:     "overlapping timestamps sums",
			existing: [][]any{{float64(1000), "10"}},
			newVals:  [][]any{{float64(1000), "5"}},
			wantLen:  1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mergeMatrixValues(tt.existing, tt.newVals)
			require.Len(t, result, tt.wantLen)

			if tt.name == "overlapping timestamps sums" {
				// value at ts 1000 should be 10+5=15
				require.Equal(t, "15", result[0][1])
			}
		})
	}
}

func TestHandleLokiVolume_SameMetricSumming(t *testing.T) {
	logger := log.NewNopLogger()

	// Two backends return the same metric key; values should be summed.
	body1 := `{"status":"success","data":{"resultType":"vector","result":[{"metric":{"__name__":"volume_bytes"},"value":[1609459200,"1000"]}]}}`
	body2 := `{"status":"success","data":{"resultType":"vector","result":[{"metric":{"__name__":"volume_bytes"},"value":[1609459200,"500"]}]}}`

	results := make(chan *proxyresponse.BackendResponse, 2)
	for _, b := range []string{body1, body2} {
		rec := httptest.NewRecorder()
		rec.WriteString(b)
		results <- wrapResponse(rec.Result())
	}
	close(results)

	w := httptest.NewRecorder()
	HandleLokiVolume(t.Context(), w, results, logger)

	var resp VolumeResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Data.Result, 1)
	require.Equal(t, "1500", resp.Data.Result[0].Value[1])
}

func TestHandleLokiVolume_NilResponseBody(t *testing.T) {
	logger := log.NewNopLogger()

	results := make(chan *proxyresponse.BackendResponse, 1)
	results <- &proxyresponse.BackendResponse{Response: nil, BackendName: "loki1"}
	close(results)

	w := httptest.NewRecorder()
	HandleLokiVolume(t.Context(), w, results, logger)

	var resp VolumeResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Empty(t, resp.Data.Result)
}

func TestHandleLokiVolumeRange_SameMetricMerging(t *testing.T) {
	logger := log.NewNopLogger()

	// Two backends: same metric key, same timestamp — values should be summed.
	body1 := `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"__name__":"volume_bytes"},"values":[[1609459200,"100"]]}]}}`
	body2 := `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"__name__":"volume_bytes"},"values":[[1609459200,"200"]]}]}}`

	results := make(chan *proxyresponse.BackendResponse, 2)
	for _, b := range []string{body1, body2} {
		rec := httptest.NewRecorder()
		rec.WriteString(b)
		results <- wrapResponse(rec.Result())
	}
	close(results)

	w := httptest.NewRecorder()
	HandleLokiVolumeRange(t.Context(), w, results, logger)

	var resp VolumeResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Data.Result, 1)
	require.Len(t, resp.Data.Result[0].Values, 1)
	require.Equal(t, "300", resp.Data.Result[0].Values[0][1])
}

func TestHandleLokiVolumeRange_InvalidJSON(t *testing.T) {
	logger := log.NewNopLogger()

	results := make(chan *proxyresponse.BackendResponse, 1)
	rec := httptest.NewRecorder()
	rec.WriteString("not valid json")
	results <- wrapResponse(rec.Result())
	close(results)

	w := httptest.NewRecorder()
	HandleLokiVolumeRange(t.Context(), w, results, logger)

	var resp VolumeResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Empty(t, resp.Data.Result)
}

func TestHandleLokiVolumeRange_ReadError(t *testing.T) {
	logger := log.NewNopLogger()

	results := make(chan *proxyresponse.BackendResponse, 1)
	results <- wrapResponse(&http.Response{StatusCode: 200, Body: &failingReader{}})
	close(results)

	w := httptest.NewRecorder()
	HandleLokiVolumeRange(t.Context(), w, results, logger)

	var resp VolumeResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Empty(t, resp.Data.Result)
}

func TestHandleLokiVolumeRange_NilResponseBody(t *testing.T) {
	logger := log.NewNopLogger()

	results := make(chan *proxyresponse.BackendResponse, 1)
	results <- &proxyresponse.BackendResponse{Response: nil, BackendName: "loki1"}
	close(results)

	w := httptest.NewRecorder()
	HandleLokiVolumeRange(t.Context(), w, results, logger)

	var resp VolumeResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Empty(t, resp.Data.Result)
}

func TestHandleLokiVolumeRange(t *testing.T) {
	logger := log.NewNopLogger()

	tests := []struct {
		name            string
		responses       []string
		expectedVolumes int
	}{
		{
			name: "single matrix response",
			responses: []string{`{
				"status": "success",
				"data": {
					"resultType": "matrix",
					"result": [
						{
							"metric": {"__name__": "volume_bytes"},
							"values": [
								[1609459200, "1024"],
								[1609459260, "2048"]
							]
						}
					]
				}
			}`},
			expectedVolumes: 1,
		},
		{
			name: "multiple matrix responses",
			responses: []string{
				`{
					"status": "success",
					"data": {
						"resultType": "matrix",
						"result": [
							{
								"metric": {"__name__": "volume_bytes", "instance": "loki1"},
								"values": [
									[1609459200, "1024"],
									[1609459260, "2048"]
								]
							}
						]
					}
				}`,
				`{
					"status": "success",
					"data": {
						"resultType": "matrix",
						"result": [
							{
								"metric": {"__name__": "volume_bytes", "instance": "loki2"},
								"values": [
									[1609459200, "512"],
									[1609459260, "1024"]
								]
							}
						]
					}
				}`,
			},
			expectedVolumes: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results := make(chan *proxyresponse.BackendResponse, len(tt.responses))
			for _, respBody := range tt.responses {
				resp := httptest.NewRecorder()
				resp.WriteString(respBody)
				results <- wrapResponse(resp.Result())
			}
			close(results)

			w := httptest.NewRecorder()
			HandleLokiVolumeRange(t.Context(), w, results, logger)

			var volumeResponse VolumeResponse
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &volumeResponse))

			require.Equal(t, statusSuccess, volumeResponse.Status)
			require.Equal(t, resultTypeMatrix, volumeResponse.Data.ResultType)
			require.Len(t, volumeResponse.Data.Result, tt.expectedVolumes)
		})
	}
}
