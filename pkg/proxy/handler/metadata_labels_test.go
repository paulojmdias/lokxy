package handler

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/go-kit/log"
	"github.com/stretchr/testify/require"

	"github.com/paulojmdias/lokxy/pkg/proxy/proxyresponse"
)

// ---- helpers ----

func labelsChannel(bodies ...string) <-chan *proxyresponse.BackendResponse {
	ch := make(chan *proxyresponse.BackendResponse, len(bodies))
	for _, b := range bodies {
		rec := httptest.NewRecorder()
		rec.WriteString(b)
		ch <- wrapResponse(rec.Result())
	}
	close(ch)
	return ch
}

func decodeLabelResponse(t *testing.T, body []byte) []string {
	t.Helper()
	var resp struct {
		Status string   `json:"status"`
		Data   []string `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &resp))
	require.Equal(t, "success", resp.Status)
	return resp.Data
}

// ---- HandleLokiLabelsWithMetadata ----

func TestHandleLokiLabelsWithMetadata_MergesFieldNames(t *testing.T) {
	labelsBody := `{"status":"success","data":["app","job"]}`
	detectedFieldsBody := `{"fields":[{"label":"trace_id","type":"string","cardinality":500},{"label":"user_id","type":"string","cardinality":200}]}`

	results := labelsChannel(labelsBody)
	w := httptest.NewRecorder()
	HandleLokiLabelsWithMetadata(context.Background(), w, results, []byte(detectedFieldsBody), nil, log.NewNopLogger())

	data := decodeLabelResponse(t, w.Body.Bytes())
	require.Contains(t, data, "app")
	require.Contains(t, data, "job")
	require.Contains(t, data, "trace_id")
	require.Contains(t, data, "user_id")
	require.Len(t, data, 4)

	// Verify sorted order
	for i := 1; i < len(data); i++ {
		require.Less(t, data[i-1], data[i], "labels must be sorted")
	}
}

func TestHandleLokiLabelsWithMetadata_AllowlistFilters(t *testing.T) {
	labelsBody := `{"status":"success","data":["app"]}`
	detectedFieldsBody := `{"fields":[
		{"label":"trace_id","type":"string","cardinality":500},
		{"label":"user_id","type":"string","cardinality":200},
		{"label":"request_id","type":"string","cardinality":100}
	]}`

	results := labelsChannel(labelsBody)
	w := httptest.NewRecorder()
	HandleLokiLabelsWithMetadata(context.Background(), w, results, []byte(detectedFieldsBody), []string{"trace_id"}, log.NewNopLogger())

	data := decodeLabelResponse(t, w.Body.Bytes())
	require.Contains(t, data, "app")
	require.Contains(t, data, "trace_id")
	require.NotContains(t, data, "user_id")
	require.NotContains(t, data, "request_id")
	require.Len(t, data, 2)
}

func TestHandleLokiLabelsWithMetadata_Deduplication(t *testing.T) {
	// trace_id appears as a real label AND a detected field — should appear once.
	labelsBody := `{"status":"success","data":["app","trace_id"]}`
	detectedFieldsBody := `{"fields":[{"label":"trace_id","type":"string","cardinality":500}]}`

	results := labelsChannel(labelsBody)
	w := httptest.NewRecorder()
	HandleLokiLabelsWithMetadata(context.Background(), w, results, []byte(detectedFieldsBody), nil, log.NewNopLogger())

	data := decodeLabelResponse(t, w.Body.Bytes())
	require.Len(t, data, 2)
	require.Contains(t, data, "app")
	require.Contains(t, data, "trace_id")
}

func TestHandleLokiLabelsWithMetadata_EmptyDetectedFields(t *testing.T) {
	labelsBody := `{"status":"success","data":["app","job"]}`

	results := labelsChannel(labelsBody)
	w := httptest.NewRecorder()
	HandleLokiLabelsWithMetadata(context.Background(), w, results, []byte{}, nil, log.NewNopLogger())

	data := decodeLabelResponse(t, w.Body.Bytes())
	require.Len(t, data, 2)
	require.Contains(t, data, "app")
	require.Contains(t, data, "job")
}

func TestHandleLokiLabelsWithMetadata_InvalidDetectedFieldsBytes(t *testing.T) {
	labelsBody := `{"status":"success","data":["app"]}`

	results := labelsChannel(labelsBody)
	w := httptest.NewRecorder()
	HandleLokiLabelsWithMetadata(context.Background(), w, results, []byte("not json"), nil, log.NewNopLogger())

	// Graceful degradation: real labels still returned.
	data := decodeLabelResponse(t, w.Body.Bytes())
	require.Len(t, data, 1)
	require.Contains(t, data, "app")
}

func TestHandleLokiLabelsWithMetadata_MultipleBackends(t *testing.T) {
	detectedFieldsBody := `{"fields":[{"label":"trace_id","type":"string","cardinality":100}]}`

	results := labelsChannel(
		`{"status":"success","data":["app","job"]}`,
		`{"status":"success","data":["env","job"]}`,
	)
	w := httptest.NewRecorder()
	HandleLokiLabelsWithMetadata(context.Background(), w, results, []byte(detectedFieldsBody), nil, log.NewNopLogger())

	data := decodeLabelResponse(t, w.Body.Bytes())
	// app, env, job (deduplicated) + trace_id = 4
	require.Len(t, data, 4)
}

func TestHandleLokiLabelsWithMetadata_EmptyAllowlistExposesAll(t *testing.T) {
	labelsBody := `{"status":"success","data":[]}`
	detectedFieldsBody := `{"fields":[
		{"label":"trace_id","type":"string","cardinality":1},
		{"label":"user_id","type":"string","cardinality":1}
	]}`

	results := labelsChannel(labelsBody)
	w := httptest.NewRecorder()
	// Empty allowlist → all fields exposed.
	HandleLokiLabelsWithMetadata(context.Background(), w, results, []byte(detectedFieldsBody), []string{}, log.NewNopLogger())

	data := decodeLabelResponse(t, w.Body.Bytes())
	require.Contains(t, data, "trace_id")
	require.Contains(t, data, "user_id")
}

// ---- HandleLokiLabelValuesWithMetadataField ----

func TestHandleLokiLabelValuesWithMetadataField_MergesValues(t *testing.T) {
	labelValuesBody := `{"status":"success","data":["frontend","backend"]}`
	detectedFieldValuesBody := `{"field":"trace_id","values":[{"value":"abc123","count":5},{"value":"def456","count":3}]}`

	results := labelsChannel(labelValuesBody)
	w := httptest.NewRecorder()
	HandleLokiLabelValuesWithMetadataField(context.Background(), w, results, []byte(detectedFieldValuesBody), log.NewNopLogger())

	data := decodeLabelResponse(t, w.Body.Bytes())
	require.Contains(t, data, "frontend")
	require.Contains(t, data, "backend")
	require.Contains(t, data, "abc123")
	require.Contains(t, data, "def456")
	require.Len(t, data, 4)

	// Verify sorted.
	for i := 1; i < len(data); i++ {
		require.Less(t, data[i-1], data[i], "values must be sorted")
	}
}

func TestHandleLokiLabelValuesWithMetadataField_Deduplication(t *testing.T) {
	// "abc123" appears in both label values and field values — should appear once.
	labelValuesBody := `{"status":"success","data":["abc123","backend"]}`
	detectedFieldValuesBody := `{"field":"trace_id","values":[{"value":"abc123","count":5},{"value":"def456","count":3}]}`

	results := labelsChannel(labelValuesBody)
	w := httptest.NewRecorder()
	HandleLokiLabelValuesWithMetadataField(context.Background(), w, results, []byte(detectedFieldValuesBody), log.NewNopLogger())

	data := decodeLabelResponse(t, w.Body.Bytes())
	require.Len(t, data, 3) // abc123, backend, def456
}

func TestHandleLokiLabelValuesWithMetadataField_EmptyFieldValues(t *testing.T) {
	labelValuesBody := `{"status":"success","data":["frontend"]}`

	results := labelsChannel(labelValuesBody)
	w := httptest.NewRecorder()
	HandleLokiLabelValuesWithMetadataField(context.Background(), w, results, []byte{}, log.NewNopLogger())

	data := decodeLabelResponse(t, w.Body.Bytes())
	require.Len(t, data, 1)
	require.Contains(t, data, "frontend")
}

func TestHandleLokiLabelValuesWithMetadataField_InvalidFieldValuesBytes(t *testing.T) {
	labelValuesBody := `{"status":"success","data":["frontend"]}`

	results := labelsChannel(labelValuesBody)
	w := httptest.NewRecorder()
	HandleLokiLabelValuesWithMetadataField(context.Background(), w, results, []byte("not json"), log.NewNopLogger())

	// Graceful degradation: real values still returned.
	data := decodeLabelResponse(t, w.Body.Bytes())
	require.Len(t, data, 1)
	require.Contains(t, data, "frontend")
}

func TestHandleLokiLabelValuesWithMetadataField_BothEmpty(t *testing.T) {
	results := labelsChannel(`{"status":"success","data":[]}`)
	w := httptest.NewRecorder()
	HandleLokiLabelValuesWithMetadataField(context.Background(), w, results, []byte(`{"field":"x","values":[]}`), log.NewNopLogger())

	data := decodeLabelResponse(t, w.Body.Bytes())
	require.Empty(t, data)
}

// ---- isFieldAllowed ----

func TestIsFieldAllowed_EmptyAllowedMeansAll(t *testing.T) {
	require.True(t, isFieldAllowed("anything", nil))
	require.True(t, isFieldAllowed("anything", []string{}))
}

func TestIsFieldAllowed_FilterWorks(t *testing.T) {
	allowed := []string{"trace_id", "user_id"}
	require.True(t, isFieldAllowed("trace_id", allowed))
	require.True(t, isFieldAllowed("user_id", allowed))
	require.False(t, isFieldAllowed("request_id", allowed))
}
