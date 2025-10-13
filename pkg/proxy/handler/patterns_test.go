package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-kit/log"
)

func TestHandleLokiPatterns_SingleResponse(t *testing.T) {
	logger := log.NewNopLogger()

	body := `{
		"status":"success",
		"data":[
			{
				"pattern":"<_> level=error method=/cortex.Ingester/Push",
				"samples":[[1711839260,1],[1711839270,2],[1711839280,1]]
			}
		]
	}`

	results := make(chan *http.Response, 1)
	rec := httptest.NewRecorder()
	rec.WriteString(body)
	results <- rec.Result()
	close(results)

	w := httptest.NewRecorder()
	HandleLokiPatterns(w, results, logger)

	var out LokiPatternsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if out.Status != "success" {
		t.Fatalf("expected status success, got %q", out.Status)
	}
	if len(out.Data) != 1 {
		t.Fatalf("expected 1 pattern, got %d", len(out.Data))
	}
	got := out.Data[0]
	if got.Pattern != "<_> level=error method=/cortex.Ingester/Push" {
		t.Fatalf("unexpected pattern: %q", got.Pattern)
	}
	// ensure samples are sorted by ts asc
	for i := 1; i < len(got.Samples); i++ {
		if got.Samples[i-1][0] > got.Samples[i][0] {
			t.Fatalf("samples not sorted by timestamp")
		}
	}
}

func TestHandleLokiPatterns_MergeAcrossBackends(t *testing.T) {
	logger := log.NewNopLogger()

	responses := []string{
		`{
			"status":"success",
			"data":[
				{"pattern":"A","samples":[[10,1],[20,2]]},
				{"pattern":"B","samples":[[10,5]]}
			]
		}`,
		`{
			"status":"success",
			"data":[
				{"pattern":"A","samples":[[20,3],[30,4]]},
				{"pattern":"C","samples":[[10,7]]}
			]
		}`,
	}

	results := make(chan *http.Response, len(responses))
	for _, s := range responses {
		rec := httptest.NewRecorder()
		rec.WriteString(s)
		results <- rec.Result()
	}
	close(results)

	w := httptest.NewRecorder()
	HandleLokiPatterns(w, results, logger)

	var out LokiPatternsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Expected patterns: A, B, C (sorted)
	if len(out.Data) != 3 {
		t.Fatalf("expected 3 patterns, got %d", len(out.Data))
	}
	if out.Data[0].Pattern != "A" || out.Data[1].Pattern != "B" || out.Data[2].Pattern != "C" {
		t.Fatalf("patterns not sorted or incorrect: %+v", out.Data)
	}

	// Pattern A timestamps: 10->1, 20->2+3=5, 30->4
	a := out.Data[0]
	wantA := map[int64]int64{10: 1, 20: 5, 30: 4}
	if len(a.Samples) != len(wantA) {
		t.Fatalf("pattern A sample length mismatch: %d", len(a.Samples))
	}
	for _, pair := range a.Samples {
		ts, cnt := pair[0], pair[1]
		if wantA[ts] != cnt {
			t.Fatalf("pattern A at ts %d: want %d got %d", ts, wantA[ts], cnt)
		}
	}
	// ensure A.samples sorted by ts
	for i := 1; i < len(a.Samples); i++ {
		if a.Samples[i-1][0] > a.Samples[i][0] {
			t.Fatalf("pattern A samples not sorted")
		}
	}
}

func TestHandleLokiPatterns_Empty(t *testing.T) {
	logger := log.NewNopLogger()

	res := `{"status":"success","data":[]}`
	results := make(chan *http.Response, 1)
	rec := httptest.NewRecorder()
	rec.WriteString(res)
	results <- rec.Result()
	close(results)

	w := httptest.NewRecorder()
	HandleLokiPatterns(w, results, logger)

	var out LokiPatternsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Data) != 0 {
		t.Fatalf("expected empty data, got %d", len(out.Data))
	}
}

func TestHandleLokiPatterns_InvalidJSON(t *testing.T) {
	logger := log.NewNopLogger()

	results := make(chan *http.Response, 1)
	rec := httptest.NewRecorder()
	rec.WriteString("not-json")
	results <- rec.Result()
	close(results)

	w := httptest.NewRecorder()
	HandleLokiPatterns(w, results, logger)

	var out LokiPatternsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// we should still return a valid (empty) success envelope
	if out.Status != "success" && out.Status != "" {
		t.Fatalf("unexpected status: %q", out.Status)
	}
	if len(out.Data) != 0 {
		t.Fatalf("expected 0 patterns, got %d", len(out.Data))
	}
}

func TestHandleLokiPatterns_ResponseReaderError(t *testing.T) {
	logger := log.NewNopLogger()

	results := make(chan *http.Response, 1)
	results <- &http.Response{
		StatusCode: 200,
		Body:       &failingPatternsReader{},
	}
	close(results)

	w := httptest.NewRecorder()
	HandleLokiPatterns(w, results, logger)

	var out LokiPatternsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Data) != 0 {
		t.Fatalf("expected 0 patterns, got %d", len(out.Data))
	}
}

// failingPatternsReader always fails on Read (simulates network/IO failure).
type failingPatternsReader struct{}

func (f *failingPatternsReader) Read([]byte) (int, error) {
	return 0, errors.New("read error")

}
func (f *failingPatternsReader) Close() error {
	return nil
}
