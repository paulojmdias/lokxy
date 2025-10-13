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
					{ "label": "job", "cardinality": 2 },
					{ "label": "instance", "cardinality": 1 }
				]
			}`},
			expectedLabels: 2,
		},
		{
			name: "multiple responses with overlapping labels",
			responses: []string{
				`{
					"detectedLabels": [
						{ "label": "job", "cardinality": 2 },
						{ "label": "instance", "cardinality": 1 }
					]
				}`,
				`{
					"detectedLabels": [
						{ "label": "job", "cardinality": 3 },
						{ "label": "service", "cardinality": 1 }
					]
				}`,
			},
			expectedLabels: 3,
		},
		{
			name:           "empty response",
			responses:      []string{`{ "detectedLabels": [] }`},
			expectedLabels: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results := make(chan *http.Response, len(tt.responses))
			for _, body := range tt.responses {
				rec := httptest.NewRecorder()
				rec.WriteString(body)
				results <- rec.Result()
			}
			close(results)

			w := httptest.NewRecorder()
			HandleLokiDetectedLabels(w, results, logger)

			var resp LokiDetectedLabelsResponse
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

			assert.Len(t, resp.DetectedLabels, tt.expectedLabels)

			// ensure sorting order
			for i := 1; i < len(resp.DetectedLabels); i++ {
				assert.LessOrEqualf(t,
					resp.DetectedLabels[i-1].Label,
					resp.DetectedLabels[i].Label,
					"Labels not sorted: %s > %s",
					resp.DetectedLabels[i-1].Label,
					resp.DetectedLabels[i].Label)
			}
		})
	}
}

func TestHandleLokiDetectedLabelsWithMerging(t *testing.T) {
	logger := log.NewNopLogger()

	responses := []string{
		`{ "detectedLabels": [ { "label": "job", "cardinality": 2 } ] }`,
		`{ "detectedLabels": [ { "label": "job", "cardinality": 3 } ] }`,
	}

	results := make(chan *http.Response, len(responses))
	for _, body := range responses {
		rec := httptest.NewRecorder()
		rec.WriteString(body)
		results <- rec.Result()
	}
	close(results)

	w := httptest.NewRecorder()
	HandleLokiDetectedLabels(w, results, logger)

	var resp LokiDetectedLabelsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	require.Len(t, resp.DetectedLabels, 1)
	label := resp.DetectedLabels[0]
	assert.Equal(t, "job", label.Label)
	assert.Equal(t, 5, label.Cardinality)
}

func TestHandleLokiDetectedLabelsWithInvalidJSON(t *testing.T) {
	logger := log.NewNopLogger()

	results := make(chan *http.Response, 1)
	rec := httptest.NewRecorder()
	rec.WriteString("invalid json")
	results <- rec.Result()
	close(results)

	w := httptest.NewRecorder()
	HandleLokiDetectedLabels(w, results, logger)

	var resp LokiDetectedLabelsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	assert.Empty(t, resp.DetectedLabels)
}

func TestHandleLokiDetectedLabelsResponseReaderError(t *testing.T) {
	logger := log.NewNopLogger()

	results := make(chan *http.Response, 1)
	resp := &http.Response{
		StatusCode: 200,
		Body:       &failingDetectedLabelsReader{},
	}
	results <- resp
	close(results)

	w := httptest.NewRecorder()
	HandleLokiDetectedLabels(w, results, logger)

	var out LokiDetectedLabelsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))

	assert.Empty(t, out.DetectedLabels)
}

// failingDetectedLabelsReader always fails on Read
type failingDetectedLabelsReader struct{}

func (f *failingDetectedLabelsReader) Read([]byte) (int, error) {
	return 0, errors.New("read error")
}

func (f *failingDetectedLabelsReader) Close() error {
	return nil
}
