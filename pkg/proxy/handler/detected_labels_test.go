package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-kit/log"
)

func TestHandleLokiDetectedLabels(t *testing.T) {
	logger := log.NewNopLogger()

	tests := []struct {
		name           string
		responses      []string
		expectedLabels int
	}{
		{
			name: "single response",
			responses: []string{`{
				"detectedLabels": [
					{
						"label": "job",
						"cardinality": 2
					},
					{
						"label": "instance",
						"cardinality": 1
					}
				]
			}`},
			expectedLabels: 2,
		},
		{
			name: "multiple responses with overlapping labels",
			responses: []string{
				`{
					"detectedLabels": [
						{
							"label": "job",
							"cardinality": 2
						},
						{
							"label": "instance",
							"cardinality": 1
						}
					]
				}`,
				`{
					"detectedLabels": [
						{
							"label": "job",
							"cardinality": 3
						},
						{
							"label": "service",
							"cardinality": 1
						}
					]
				}`,
			},
			expectedLabels: 3,
		},
		{
			name: "empty response",
			responses: []string{`{
				"detectedLabels": []
			}`},
			expectedLabels: 0,
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
			HandleLokiDetectedLabels(w, results, logger)

			// Parse the response
			var detectedLabelsResponse LokiDetectedLabelsResponse
			if err := json.Unmarshal(w.Body.Bytes(), &detectedLabelsResponse); err != nil {
				t.Fatalf("Failed to unmarshal response: %v", err)
			}

			// Verify the response
			if len(detectedLabelsResponse.DetectedLabels) != tt.expectedLabels {
				t.Errorf("Expected %d labels, got %d", tt.expectedLabels, len(detectedLabelsResponse.DetectedLabels))
			}

			// Verify labels are sorted
			for i := 1; i < len(detectedLabelsResponse.DetectedLabels); i++ {
				if detectedLabelsResponse.DetectedLabels[i-1].Label > detectedLabelsResponse.DetectedLabels[i].Label {
					t.Errorf("Labels are not sorted: %s > %s",
						detectedLabelsResponse.DetectedLabels[i-1].Label,
						detectedLabelsResponse.DetectedLabels[i].Label)
				}
			}
		})
	}
}

func TestHandleLokiDetectedLabelsWithMerging(t *testing.T) {
	logger := log.NewNopLogger()

	// Test merging logic specifically - cardinalities should be summed
	responses := []string{
		`{
			"detectedLabels": [
				{
					"label": "job",
					"cardinality": 2
				}
			]
		}`,
		`{
			"detectedLabels": [
				{
					"label": "job",
					"cardinality": 3
				}
			]
		}`,
	}

	// Create a channel to simulate responses
	results := make(chan *http.Response, len(responses))

	// Create mock responses
	for _, respBody := range responses {
		resp := httptest.NewRecorder()
		resp.WriteString(respBody)
		results <- resp.Result()
	}
	close(results)

	// Create a response recorder
	w := httptest.NewRecorder()

	// Call the handler
	HandleLokiDetectedLabels(w, results, logger)

	// Parse the response
	var detectedLabelsResponse LokiDetectedLabelsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &detectedLabelsResponse); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	// Should have 1 label with cardinality = 2 + 3 = 5
	if len(detectedLabelsResponse.DetectedLabels) != 1 {
		t.Errorf("Expected 1 label, got %d", len(detectedLabelsResponse.DetectedLabels))
	}

	jobLabel := detectedLabelsResponse.DetectedLabels[0]
	if jobLabel.Label != "job" {
		t.Errorf("Expected label 'job', got %s", jobLabel.Label)
	}

	expectedCardinality := 5
	if jobLabel.Cardinality != expectedCardinality {
		t.Errorf("Expected cardinality %d, got %d", expectedCardinality, jobLabel.Cardinality)
	}
}

func TestHandleLokiDetectedLabelsWithInvalidJSON(t *testing.T) {
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
	HandleLokiDetectedLabels(w, results, logger)

	// Should return empty result but valid JSON
	var detectedLabelsResponse LokiDetectedLabelsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &detectedLabelsResponse); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if len(detectedLabelsResponse.DetectedLabels) != 0 {
		t.Errorf("Expected 0 labels, got %d", len(detectedLabelsResponse.DetectedLabels))
	}
}

func TestHandleLokiDetectedLabelsResponseReaderError(t *testing.T) {
	logger := log.NewNopLogger()

	// Create a response with a reader that will fail
	results := make(chan *http.Response, 1)
	resp := &http.Response{
		StatusCode: 200,
		Body:       &failingDetectedLabelsReader{},
	}
	results <- resp
	close(results)

	// Create a response recorder
	w := httptest.NewRecorder()

	// Call the handler
	HandleLokiDetectedLabels(w, results, logger)

	// Should return empty result but valid JSON
	var detectedLabelsResponse LokiDetectedLabelsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &detectedLabelsResponse); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if len(detectedLabelsResponse.DetectedLabels) != 0 {
		t.Errorf("Expected 0 labels, got %d", len(detectedLabelsResponse.DetectedLabels))
	}
}

// failingDetectedLabelsReader is a helper type that always returns an error when read
type failingDetectedLabelsReader struct{}

func (f *failingDetectedLabelsReader) Read([]byte) (int, error) {
	return 0, errors.New("read error")
}

func (f *failingDetectedLabelsReader) Close() error {
	return nil
}
