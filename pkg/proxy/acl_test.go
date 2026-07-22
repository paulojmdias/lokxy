package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/go-kit/log"
	"github.com/stretchr/testify/require"

	"github.com/paulojmdias/lokxy/pkg/acl"
)

// blockEmptyACL is an ACL config that rejects empty/catch-all selectors.
func blockEmptyACL() acl.ACLConfig {
	return acl.ACLConfig{
		Enabled: true,
		Rules: []acl.Rule{{
			Name:        "block-empty",
			Action:      acl.ActionBlock,
			Enforcement: acl.EnforcementEnforce,
			Reason:      "Queries must include at least one stream selector",
			When:        []acl.MatchCondition{{EmptySelector: true}},
		}},
	}
}

func TestACL_BlocksBroadQueryBeforeFanout(t *testing.T) {
	logger := log.NewNopLogger()

	var upstreamHits int32
	upstream := mkUpstreamServer(t, map[string]http.HandlerFunc{
		"/loki/api/v1/query_range": func(w http.ResponseWriter, _ *http.Request) {
			atomic.AddInt32(&upstreamHits, 1)
			io.WriteString(w, `{"status":"success"}`)
		},
	})
	defer upstream.Close()

	config := mkConfig(upstream.URL)
	config.ACL = blockEmptyACL()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, `/loki/api/v1/query_range?query={app=~".*"}`, nil)
	mustMux(t, logger, config).ServeHTTP(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code)
	require.Contains(t, rr.Body.String(), "stream selector")
	require.Equal(t, int32(0), atomic.LoadInt32(&upstreamHits), "broad query must not reach the upstream")
}

func TestACL_ForwardsWellScopedQuery(t *testing.T) {
	logger := log.NewNopLogger()

	var upstreamHits int32
	upstream := mkUpstreamServer(t, map[string]http.HandlerFunc{
		"/loki/api/v1/query_range": func(w http.ResponseWriter, _ *http.Request) {
			atomic.AddInt32(&upstreamHits, 1)
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"status":"success","data":{"resultType":"streams","result":[]}}`)
		},
	})
	defer upstream.Close()

	config := mkConfig(upstream.URL)
	config.ACL = blockEmptyACL()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, `/loki/api/v1/query_range?query={app="x"}`, nil)
	mustMux(t, logger, config).ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, int32(1), atomic.LoadInt32(&upstreamHits), "well-scoped query must reach the upstream")
}

// TestACL_TakesEffectOnReload verifies that ApplyConfig swaps the ACL engine so
// new rules apply to subsequent requests without rebuilding the mux.
func TestACL_TakesEffectOnReload(t *testing.T) {
	logger := log.NewNopLogger()

	upstream := mkUpstreamServer(t, map[string]http.HandlerFunc{
		"/loki/api/v1/query_range": func(w http.ResponseWriter, _ *http.Request) {
			io.WriteString(w, `{"status":"success"}`)
		},
	})
	defer upstream.Close()

	// Start with ACL disabled.
	base := mkConfig(upstream.URL)
	p, err := New(logger, base)
	require.NoError(t, err)
	mux := NewServeMux(logger, p, nil, false)

	// A broad query is allowed while ACL is off.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, `/loki/api/v1/query_range?query={app=~".*"}`, nil)
	mux.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	// Reload with an enforcing ACL.
	withACL := mkConfig(upstream.URL)
	withACL.ACL = blockEmptyACL()
	require.NoError(t, p.ApplyConfig(withACL))

	// The same broad query is now rejected, using the same mux.
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, `/loki/api/v1/query_range?query={app=~".*"}`, nil)
	mux.ServeHTTP(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)
}
