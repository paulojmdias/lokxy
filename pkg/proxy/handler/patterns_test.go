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

	results := make(chan *proxyresponse.BackendResponse, 1)
	rec := httptest.NewRecorder()
	rec.WriteString(body)
	results <- wrapResponse(rec.Result())
	close(results)

	w := httptest.NewRecorder()
	HandleLokiPatterns(t.Context(), w, results, logger)

	var out LokiPatternsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))

	require.Equal(t, "success", out.Status)
	require.Len(t, out.Data, 1)

	got := out.Data[0]
	require.Equal(t, "<_> level=error method=/cortex.Ingester/Push", got.Pattern)

	// ensure samples are sorted by ts asc
	for i := 1; i < len(got.Samples); i++ {
		require.LessOrEqual(t, got.Samples[i-1][0], got.Samples[i][0], "samples not sorted by timestamp")
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

	results := make(chan *proxyresponse.BackendResponse, len(responses))
	for _, s := range responses {
		rec := httptest.NewRecorder()
		rec.WriteString(s)
		results <- wrapResponse(rec.Result())
	}
	close(results)

	w := httptest.NewRecorder()
	HandleLokiPatterns(t.Context(), w, results, logger)

	var out LokiPatternsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))

	// Expected patterns: A, B, C (sorted)
	require.Len(t, out.Data, 3)
	require.Equal(t, "A", out.Data[0].Pattern)
	require.Equal(t, "B", out.Data[1].Pattern)
	require.Equal(t, "C", out.Data[2].Pattern)

	// Pattern A timestamps: 10->1, 20->2+3=5, 30->4
	a := out.Data[0]
	wantA := map[int64]int64{10: 1, 20: 5, 30: 4}
	require.Len(t, a.Samples, len(wantA))
	gotA := map[int64]int64{}
	for _, pair := range a.Samples {
		ts, cnt := pair[0], pair[1]
		gotA[ts] = cnt
	}
	require.Equal(t, wantA, gotA)

	// ensure A.samples sorted by ts
	for i := 1; i < len(a.Samples); i++ {
		require.LessOrEqual(t, a.Samples[i-1][0], a.Samples[i][0], "pattern A samples not sorted")
	}
}

func TestHandleLokiPatterns_Empty(t *testing.T) {
	logger := log.NewNopLogger()

	res := `{"status":"success","data":[]}`
	results := make(chan *proxyresponse.BackendResponse, 1)
	rec := httptest.NewRecorder()
	rec.WriteString(res)
	results <- wrapResponse(rec.Result())
	close(results)

	w := httptest.NewRecorder()
	HandleLokiPatterns(t.Context(), w, results, logger)

	var out LokiPatternsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	require.Empty(t, out.Data)
}

func TestHandleLokiPatterns_InvalidJSON(t *testing.T) {
	logger := log.NewNopLogger()

	results := make(chan *proxyresponse.BackendResponse, 1)
	rec := httptest.NewRecorder()
	rec.WriteString("not-json")
	results <- wrapResponse(rec.Result())
	close(results)

	w := httptest.NewRecorder()
	HandleLokiPatterns(t.Context(), w, results, logger)

	var out LokiPatternsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	// we should still return a valid (empty) success envelope
	require.True(t, out.Status == "success" || out.Status == "")
	require.Empty(t, out.Data)
}

func TestHandleLokiPatterns_ResponseReaderError(t *testing.T) {
	logger := log.NewNopLogger()

	results := make(chan *proxyresponse.BackendResponse, 1)
	results <- wrapResponse(&http.Response{
		StatusCode: 200,
		Body:       &failingPatternsReader{},
	})
	close(results)

	w := httptest.NewRecorder()
	HandleLokiPatterns(t.Context(), w, results, logger)

	var out LokiPatternsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	require.Empty(t, out.Data)
}

// failingPatternsReader always fails on Read (simulates network/IO failure).
type failingPatternsReader struct{}

func (f *failingPatternsReader) Read([]byte) (int, error) {
	return 0, errors.New("read error")
}

func (f *failingPatternsReader) Close() error {
	return nil
}
