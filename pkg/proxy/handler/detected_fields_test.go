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

// ---- Helpers ----
// Renamed to avoid collision with failingReader in other tests
type failingDFReader struct{}

func (f *failingDFReader) Read([]byte) (int, error) {
	return 0, errors.New("read error")
}

func (f *failingDFReader) Close() error {
	return nil
}

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
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))

	require.NotNil(t, out.Limit)
	assert.Equal(t, 1000, *out.Limit)
	assert.Len(t, out.Fields, 2)

	// ensure sort by label
	for i := 1; i < len(out.Fields); i++ {
		assert.LessOrEqual(t, out.Fields[i-1].Label, out.Fields[i].Label, "fields not sorted")
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
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))

	want := map[string]int{"job": 5, "instance": 1, "service": 4}
	assert.Len(t, out.Fields, len(want))
	got := map[string]int{}
	for _, f := range out.Fields {
		got[f.Label] = f.Cardinality
	}
	assert.Equal(t, want, got)
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
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))

	require.Len(t, out.Fields, 1)
	app := out.Fields[0]
	assert.Equal(t, 5, app.Cardinality)
	assert.Equal(t, "string", app.Type)
	// union of parsers = ["json","logfmt"] sorted
	assert.Equal(t, []string{"json", "logfmt"}, app.Parsers)
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
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.Empty(t, out.Fields)
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
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))

	assert.Equal(t, fieldName, out.Field)
	for i := 1; i < len(out.Values); i++ {
		assert.LessOrEqual(t, out.Values[i-1].Value, out.Values[i].Value, "values not sorted")
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
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))

	want := map[string]int{"api": 5, "worker": 1, "scheduler": 4}
	assert.Len(t, out.Values, len(want))
	got := map[string]int{}
	for _, v := range out.Values {
		got[v.Value] = v.Count
	}
	assert.Equal(t, want, got)
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
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.Empty(t, out.Values)
}
