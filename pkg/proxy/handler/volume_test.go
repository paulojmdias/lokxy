package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-kit/log"
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
			expectedStatus:  "success",
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
			// Create a channel to simulate responses
			results := make(chan *http.Response, len(tt.responses))

			// Create mock responses
			for _, respBody := range tt.responses {
				resp := httptest.NewRecorder()
				resp.WriteString(respBody)
				results <- resp.Result()
			}
			close(results)

			// Create a response recorder
			w := httptest.NewRecorder()

			// Call the handler
			HandleLokiVolume(w, results, logger)

			// Parse the response
			var volumeResponse VolumeResponse
			if err := json.Unmarshal(w.Body.Bytes(), &volumeResponse); err != nil {
				t.Fatalf("Failed to unmarshal response: %v", err)
			}

			// Verify the response
			if volumeResponse.Status != tt.expectedStatus {
				t.Errorf("Expected status %s, got %s", tt.expectedStatus, volumeResponse.Status)
			}

			if len(volumeResponse.Data.Result) != tt.expectedVolumes {
				t.Errorf("Expected %d volumes, got %d", tt.expectedVolumes, len(volumeResponse.Data.Result))
			}
		})
	}
}

func TestHandleLokiVolumeWithInvalidJSON(t *testing.T) {
	logger := log.NewNopLogger()

	// Create a channel with invalid JSON response
	results := make(chan *http.Response, 1)
	resp := httptest.NewRecorder()
	resp.WriteString("invalid json")
	results <- resp.Result()
	close(results)

	// Create a response recorder
	w := httptest.NewRecorder()

	// Call the handler
	HandleLokiVolume(w, results, logger)

	// Should return empty result but valid JSON
	var volumeResponse VolumeResponse
	if err := json.Unmarshal(w.Body.Bytes(), &volumeResponse); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if volumeResponse.Status != statusSuccess {
		t.Errorf("Expected status success, got %s", volumeResponse.Status)
	}

	if len(volumeResponse.Data.Result) != 0 {
		t.Errorf("Expected 0 volumes, got %d", len(volumeResponse.Data.Result))
	}
}

func TestHandleLokiVolumeResponseReaderError(t *testing.T) {
	logger := log.NewNopLogger()

	// Create a response with a reader that will fail
	results := make(chan *http.Response, 1)
	resp := &http.Response{
		StatusCode: 200,
		Body:       &failingReader{},
	}
	results <- resp
	close(results)

	// Create a response recorder
	w := httptest.NewRecorder()

	// Call the handler
	HandleLokiVolume(w, results, logger)

	// Should return empty result but valid JSON
	var volumeResponse VolumeResponse
	if err := json.Unmarshal(w.Body.Bytes(), &volumeResponse); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if volumeResponse.Status != statusSuccess {
		t.Errorf("Expected status success, got %s", volumeResponse.Status)
	}

	if len(volumeResponse.Data.Result) != 0 {
		t.Errorf("Expected 0 volumes, got %d", len(volumeResponse.Data.Result))
	}
}

// failingReader is a helper type that always returns an error when read
type failingReader struct{}

func (f *failingReader) Read([]byte) (int, error) {
	return 0, errors.New("read error")
}

func (f *failingReader) Close() error {
	return nil
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
			// Create a channel to simulate responses
			results := make(chan *http.Response, len(tt.responses))

			// Create mock responses
			for _, respBody := range tt.responses {
				resp := httptest.NewRecorder()
				resp.WriteString(respBody)
				results <- resp.Result()
			}
			close(results)

			// Create a response recorder
			w := httptest.NewRecorder()

			// Call the handler
			HandleLokiVolumeRange(w, results, logger)

			// Parse the response
			var volumeResponse VolumeResponse
			if err := json.Unmarshal(w.Body.Bytes(), &volumeResponse); err != nil {
				t.Fatalf("Failed to unmarshal response: %v", err)
			}

			// Verify the response
			if volumeResponse.Status != "success" {
				t.Errorf("Expected status success, got %s", volumeResponse.Status)
			}

			if volumeResponse.Data.ResultType != resultTypeMatrix {
				t.Errorf("Expected resultType matrix, got %s", volumeResponse.Data.ResultType)
			}

			if len(volumeResponse.Data.Result) != tt.expectedVolumes {
				t.Errorf("Expected %d volumes, got %d", tt.expectedVolumes, len(volumeResponse.Data.Result))
			}
		})
	}
}
