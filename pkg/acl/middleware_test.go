package acl

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// nextRecorder is a downstream handler that records whether it was reached.
type nextRecorder struct{ called bool }

func (n *nextRecorder) ServeHTTP(http.ResponseWriter, *http.Request) { n.called = true }

func blockEmptyEngine() *Engine {
	return NewEngine(ACLConfig{
		Enabled: true,
		Rules: []Rule{{
			Name:        "block-empty",
			Action:      ActionBlock,
			Enforcement: EnforcementEnforce,
			Reason:      "Queries must include at least one stream selector",
			When:        []MatchCondition{{EmptySelector: true}},
		}},
	})
}

func TestMiddleware_DisabledPassesThrough(t *testing.T) {
	next := &nextRecorder{}
	h := Middleware(func() *Engine { return nil }, nopLogger())(next)

	r := httptest.NewRequest("GET", `/loki/api/v1/query_range?query={}`, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	require.True(t, next.called)
	require.Equal(t, http.StatusOK, w.Code)
}

func TestMiddleware_PassthroughEndpointNotEvaluated(t *testing.T) {
	next := &nextRecorder{}
	h := Middleware(func() *Engine { return blockEmptyEngine() }, nopLogger())(next)

	// /labels is not an enforced endpoint; even a broad query passes.
	r := httptest.NewRequest("GET", `/loki/api/v1/labels?query={}`, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	require.True(t, next.called)
	require.Equal(t, http.StatusOK, w.Code)
}

func TestMiddleware_EnforcedEndpointRejects(t *testing.T) {
	next := &nextRecorder{}
	h := Middleware(func() *Engine { return blockEmptyEngine() }, nopLogger())(next)

	r := httptest.NewRequest("GET", `/loki/api/v1/query_range?query={app=~".*"}`, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	require.False(t, next.called)
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var body map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, "error", body["status"])
	require.Contains(t, body["error"], "stream selector")
}

func TestMiddleware_WellScopedQueryForwarded(t *testing.T) {
	next := &nextRecorder{}
	h := Middleware(func() *Engine { return blockEmptyEngine() }, nopLogger())(next)

	r := httptest.NewRequest("GET", `/loki/api/v1/query_range?query={app="x"}`, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	require.True(t, next.called)
	require.Equal(t, http.StatusOK, w.Code)
}

func TestMiddleware_WarnModeForwardsWithHeader(t *testing.T) {
	eng := NewEngine(ACLConfig{
		Enabled: true,
		Rules: []Rule{{
			Name:        "warn-empty",
			Action:      ActionBlock,
			Enforcement: EnforcementWarn,
			When:        []MatchCondition{{EmptySelector: true}},
		}},
	})
	next := &nextRecorder{}
	h := Middleware(func() *Engine { return eng }, nopLogger())(next)

	r := httptest.NewRequest("GET", `/loki/api/v1/query_range?query={}`, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	require.True(t, next.called)
	require.Equal(t, "warn-empty", w.Header().Get(warningHeader))
}

func TestMiddleware_POSTBodyPreservedForFanout(t *testing.T) {
	// The downstream handler must still be able to read the full body.
	var seenBody string
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		seenBody = string(b)
	})
	h := Middleware(func() *Engine { return blockEmptyEngine() }, nopLogger())(next)

	body := `query={app="x"}`
	r := httptest.NewRequest("POST", "/loki/api/v1/query_range", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, body, seenBody)
}

func TestMiddleware_UnparseableQueryFailsOpen(t *testing.T) {
	next := &nextRecorder{}
	h := Middleware(func() *Engine { return blockEmptyEngine() }, nopLogger())(next)

	r := httptest.NewRequest("GET", `/loki/api/v1/query_range?query={app=`, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	// Parse failure => fail open, request forwarded.
	require.True(t, next.called)
}

func TestMiddleware_TailEndpointEnforced(t *testing.T) {
	next := &nextRecorder{}
	h := Middleware(func() *Engine { return blockEmptyEngine() }, nopLogger())(next)

	r := httptest.NewRequest("GET", `/loki/api/v1/tail?expr={app=~".*"}`, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	require.False(t, next.called)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestMiddleware_MissingQueryParamPassesThrough(t *testing.T) {
	next := &nextRecorder{}
	h := Middleware(func() *Engine { return blockEmptyEngine() }, nopLogger())(next)

	// Enforced endpoint but no query parameter at all: nothing to evaluate.
	r := httptest.NewRequest("GET", `/loki/api/v1/query_range`, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	require.True(t, next.called)
	require.Equal(t, http.StatusOK, w.Code)
}

func TestMiddleware_EmptyQueryValuePassesThrough(t *testing.T) {
	next := &nextRecorder{}
	h := Middleware(func() *Engine { return blockEmptyEngine() }, nopLogger())(next)

	r := httptest.NewRequest("GET", `/loki/api/v1/query_range?query=`, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	require.True(t, next.called)
	require.Equal(t, http.StatusOK, w.Code)
}

// errReader always fails, to exercise the body-read failure path.
type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestMiddleware_BodyReadErrorFailsOpen(t *testing.T) {
	next := &nextRecorder{}
	h := Middleware(func() *Engine { return blockEmptyEngine() }, nopLogger())(next)

	r := httptest.NewRequest("POST", "/loki/api/v1/query_range", errReader{})
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	// Body could not be read; fail open rather than reject.
	require.True(t, next.called)
}

// failWriter is a ResponseWriter whose body writes always fail, to exercise the
// error branch in writeRejection.
type failWriter struct{ header http.Header }

func (f *failWriter) Header() http.Header {
	if f.header == nil {
		f.header = http.Header{}
	}
	return f.header
}
func (f *failWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func (f *failWriter) WriteHeader(int)           {}

func TestWriteRejection_WriteErrorIsHandled(t *testing.T) {
	// Must not panic when the response body cannot be written.
	writeRejection(&failWriter{}, "nope", nopLogger())
}
