package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-kit/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
			results := make(chan *http.Response, len(tt.responses))

			for _, respBody := range tt.responses {
				resp := httptest.NewRecorder()
				resp.WriteString(respBody)
				results <- resp.Result()
			}
			close(results)

			w := httptest.NewRecorder()
			HandleLokiVolume(t.Context(), w, results, logger)

			var volumeResponse VolumeResponse
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &volumeResponse))

			assert.Equal(t, tt.expectedStatus, volumeResponse.Status)
			assert.Len(t, volumeResponse.Data.Result, tt.expectedVolumes)
		})
	}
}

func TestHandleLokiVolumeWithInvalidJSON(t *testing.T) {
	logger := log.NewNopLogger()

	results := make(chan *http.Response, 1)
	resp := httptest.NewRecorder()
	resp.WriteString("invalid json")
	results <- resp.Result()
	close(results)

	w := httptest.NewRecorder()
	HandleLokiVolume(t.Context(), w, results, logger)

	var volumeResponse VolumeResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &volumeResponse))

	assert.Equal(t, statusSuccess, volumeResponse.Status)
	assert.Empty(t, volumeResponse.Data.Result)
}

func TestHandleLokiVolumeResponseReaderError(t *testing.T) {
	logger := log.NewNopLogger()

	results := make(chan *http.Response, 1)
	resp := &http.Response{
		StatusCode: 200,
		Body:       &failingReader{},
	}
	results <- resp
	close(results)

	w := httptest.NewRecorder()
	HandleLokiVolume(t.Context(), w, results, logger)

	var volumeResponse VolumeResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &volumeResponse))

	assert.Equal(t, statusSuccess, volumeResponse.Status)
	assert.Empty(t, volumeResponse.Data.Result)
}

// failingReader is a helper that always returns an error
type failingReader struct{}

func (f *failingReader) Read([]byte) (int, error) {
	return 0, errors.New("read error")
}
func (f *failingReader) Close() error { return nil }

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
			results := make(chan *http.Response, len(tt.responses))
			for _, respBody := range tt.responses {
				resp := httptest.NewRecorder()
				resp.WriteString(respBody)
				results <- resp.Result()
			}
			close(results)

			w := httptest.NewRecorder()
			HandleLokiVolumeRange(t.Context(), w, results, logger)

			var volumeResponse VolumeResponse
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &volumeResponse))

			assert.Equal(t, statusSuccess, volumeResponse.Status)
			assert.Equal(t, resultTypeMatrix, volumeResponse.Data.ResultType)
			assert.Len(t, volumeResponse.Data.Result, tt.expectedVolumes)
		})
	}
}
