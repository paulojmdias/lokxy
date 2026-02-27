package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-kit/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/paulojmdias/lokxy/pkg/config"
)

func TestNewServeMux_Healthy(t *testing.T) {
	logger := log.NewNopLogger()
	cfg := &config.Config{
		ServerGroups: []config.ServerGroup{{Name: "loki1", URL: "http://localhost:3100"}},
	}

	mux := NewServeMux(logger, cfg)
	req := httptest.NewRequest(http.MethodGet, "/healthy", nil)
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body, err := io.ReadAll(w.Body)
	require.NoError(t, err)
	assert.Equal(t, "OK", string(body))
}

func TestNewServeMux_Ready_WhenReady(t *testing.T) {
	logger := log.NewNopLogger()
	cfg := &config.Config{
		ServerGroups: []config.ServerGroup{{Name: "loki1", URL: "http://localhost:3100"}},
	}

	config.SetReady(true)
	t.Cleanup(func() { config.SetReady(false) })

	mux := NewServeMux(logger, cfg)
	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body, err := io.ReadAll(w.Body)
	require.NoError(t, err)
	assert.Equal(t, "OK", string(body))
}

func TestNewServeMux_Ready_WhenNotReady(t *testing.T) {
	logger := log.NewNopLogger()
	cfg := &config.Config{
		ServerGroups: []config.ServerGroup{{Name: "loki1", URL: "http://localhost:3100"}},
	}

	config.SetReady(false)

	mux := NewServeMux(logger, cfg)
	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)

	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	body, err := io.ReadAll(w.Body)
	require.NoError(t, err)
	assert.Equal(t, "Not Ready", string(body))
}

func TestNewServeMux_ProxyCatchAll(t *testing.T) {
	// Set up a mock upstream backend
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("upstream ok"))
	}))
	defer upstream.Close()

	logger := log.NewNopLogger()
	cfg := &config.Config{
		ServerGroups: []config.ServerGroup{
			{Name: "loki1", URL: upstream.URL},
		},
	}

	mux := NewServeMux(logger, cfg)

	// Hit an unknown path that falls through to the catch-all proxy handler
	req := httptest.NewRequest(http.MethodGet, "/some/unknown/path", nil)
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)

	// The catch-all uses forwardFirstResponse which forwards the upstream's response
	require.Equal(t, http.StatusOK, w.Code)
}
