package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-kit/log"
)

// ---- Helpers ----
// Renamed to avoid collision with failingReader in volume_test.go
type failingDFReader struct{}

func (f *failingDFReader) Read([]byte) (int, error) { return 0, errors.New("read error") }
func (f *failingDFReader) Close() error             { return nil }

// ----------------- /detected_fields tests -----------------

func TestDetectedFields_VariantA_Single(t *testing.T) {
	logger := log.NewNopLogger()
	body := `{
		"fields":[
			{"label":"app","type":"string","cardinality":3,"parsers":["logfmt"]},
			{"label":"instance","type":"string","cardinality":2,"parsers":null}
		],
		"limit": 1000
	}`

	results := make(chan *http.Response, 1)
	rec := httptest.NewRecorder()
	rec.WriteString(body)
	results <- rec.Result()
	close(results)

	w := httptest.NewRecorder()
	HandleLokiDetectedFields(w, results, logger)

	var out LokiDetectedFieldsOut
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if out.Limit == nil || *out.Limit != 1000 {
		t.Fatalf("expected limit 1000, got %#v", out.Limit)
	}
	if len(out.Fields) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(out.Fields))
	}
	// ensure sort by label
	for i := 1; i < len(out.Fields); i++ {
		if out.Fields[i-1].Label > out.Fields[i].Label {
			t.Fatalf("fields not sorted")
		}
	}
}

func TestDetectedFields_VariantB_Merge(t *testing.T) {
	logger := log.NewNopLogger()

	responses := []string{
		`{"detectedFields":[{"field":"job","cardinality":2},{"field":"instance","cardinality":1}]}`,
		`{"detectedFields":[{"label":"job","cardinality":3},{"field":"service","cardinality":4}]}`,
	}

	results := make(chan *http.Response, len(responses))
	for _, s := range responses {
		rec := httptest.NewRecorder()
		rec.WriteString(s)
		results <- rec.Result()
	}
	close(results)

	w := httptest.NewRecorder()
	HandleLokiDetectedFields(w, results, logger)

	var out LokiDetectedFieldsOut
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := map[string]int{"job": 5, "instance": 1, "service": 4}
	if len(out.Fields) != len(want) {
		t.Fatalf("expected %d fields, got %d", len(want), len(out.Fields))
	}
	for _, f := range out.Fields {
		if want[f.Label] != f.Cardinality {
			t.Fatalf("label %s: want %d got %d", f.Label, want[f.Label], f.Cardinality)
		}
	}
}

func TestDetectedFields_ParsersUnionAndType(t *testing.T) {
	logger := log.NewNopLogger()
	responses := []string{
		`{"fields":[{"label":"app","type":"string","cardinality":2,"parsers":["logfmt"]}]}`,
		`{"fields":[{"label":"app","type":"","cardinality":3,"parsers":["json","logfmt"]}]}`,
	}

	results := make(chan *http.Response, len(responses))
	for _, s := range responses {
		rec := httptest.NewRecorder()
		rec.WriteString(s)
		results <- rec.Result()
	}
	close(results)

	w := httptest.NewRecorder()
	HandleLokiDetectedFields(w, results, logger)

	var out LokiDetectedFieldsOut
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Fields) != 1 {
		t.Fatalf("expected 1 field, got %d", len(out.Fields))
	}
	app := out.Fields[0]
	if app.Cardinality != 5 {
		t.Fatalf("cardinality want 5 got %d", app.Cardinality)
	}
	if app.Type != "string" {
		t.Fatalf("type want string got %q", app.Type)
	}
	// union of parsers = ["json","logfmt"] sorted
	want := []string{"json", "logfmt"}
	if len(app.Parsers) != 2 || app.Parsers[0] != want[0] || app.Parsers[1] != want[1] {
		t.Fatalf("parsers want %v got %v", want, app.Parsers)
	}
}

func TestDetectedFields_InvalidJSONAndReaderErr(t *testing.T) {
	logger := log.NewNopLogger()

	results := make(chan *http.Response, 2)
	rec1 := httptest.NewRecorder()
	rec1.WriteString(`not-json`)
	results <- rec1.Result()
	results <- &http.Response{StatusCode: 200, Body: &failingDFReader{}}
	close(results)

	w := httptest.NewRecorder()
	HandleLokiDetectedFields(w, results, logger)

	var out LokiDetectedFieldsOut
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Fields) != 0 {
		t.Fatalf("expected 0 fields, got %d", len(out.Fields))
	}
}

// ----------------- /detected_field/{name}/values tests -----------------

func TestDetectedFieldValues_SingleAndSorted(t *testing.T) {
	logger := log.NewNopLogger()
	fieldName := "job"

	body := `{
		"label":"job",
		"values":[
			{"value":"api","count":2},
			{"value":"worker","count":1}
		]
	}`

	results := make(chan *http.Response, 1)
	rec := httptest.NewRecorder()
	rec.WriteString(body)
	results <- rec.Result()
	close(results)

	w := httptest.NewRecorder()
	HandleLokiDetectedFieldValues(w, results, fieldName, logger)

	var out LokiDetectedFieldValuesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Field != fieldName {
		t.Fatalf("field name mismatch: want %s got %s", fieldName, out.Field)
	}
	// sorted by value
	for i := 1; i < len(out.Values); i++ {
		if out.Values[i-1].Value > out.Values[i].Value {
			t.Fatalf("values not sorted")
		}
	}
}

func TestDetectedFieldValues_MergeAcrossBackends(t *testing.T) {
	logger := log.NewNopLogger()
	fieldName := "job"

	responses := []string{
		`{"field":"job","values":[{"value":"api","count":2},{"value":"worker","count":1}]}`,
		`{"label":"job","values":[{"value":"api","count":3},{"value":"scheduler","count":4}]}`,
	}

	results := make(chan *http.Response, len(responses))
	for _, s := range responses {
		rec := httptest.NewRecorder()
		rec.WriteString(s)
		results <- rec.Result()
	}
	close(results)

	w := httptest.NewRecorder()
	HandleLokiDetectedFieldValues(w, results, fieldName, logger)

	var out LokiDetectedFieldValuesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	want := map[string]int{"api": 5, "worker": 1, "scheduler": 4}
	if len(out.Values) != len(want) {
		t.Fatalf("expected %d values, got %d", len(want), len(out.Values))
	}
	for _, v := range out.Values {
		if want[v.Value] != v.Count {
			t.Fatalf("value %s: want %d got %d", v.Value, want[v.Value], v.Count)
		}
	}
}

func TestDetectedFieldValues_InvalidJSONAndReaderErr(t *testing.T) {
	logger := log.NewNopLogger()
	fieldName := "env"

	results := make(chan *http.Response, 2)
	rec1 := httptest.NewRecorder()
	rec1.WriteString(`oops`)
	results <- rec1.Result()
	results <- &http.Response{StatusCode: 200, Body: &failingDFReader{}}
	close(results)

	w := httptest.NewRecorder()
	HandleLokiDetectedFieldValues(w, results, fieldName, logger)

	var out LokiDetectedFieldValuesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Values) != 0 {
		t.Fatalf("expected 0 values, got %d", len(out.Values))
	}
}
