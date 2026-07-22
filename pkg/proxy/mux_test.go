package proxy

import (
	"errors"
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

	mux := mustMux(t, logger, cfg)
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

	mux := mustMux(t, logger, cfg)
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

	mux := mustMux(t, logger, cfg)
	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)

	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	body, err := io.ReadAll(w.Body)
	require.NoError(t, err)
	assert.Equal(t, "Not Ready", string(body))
}

func TestNewServeMux_Reload_Success(t *testing.T) {
	logger := log.NewNopLogger()
	cfg := &config.Config{
		ServerGroups: []config.ServerGroup{{Name: "loki1", URL: "http://localhost:3100"}},
	}

	p, err := New(logger, cfg)
	require.NoError(t, err)

	called := false
	mux := NewServeMux(logger, p, func() error {
		called = true
		return nil
	}, true)

	req := httptest.NewRequest(http.MethodPost, "/-/reload", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.True(t, called)
	assert.Contains(t, w.Body.String(), "config reloaded")
}

func TestNewServeMux_Reload_Failure(t *testing.T) {
	logger := log.NewNopLogger()
	cfg := &config.Config{
		ServerGroups: []config.ServerGroup{{Name: "loki1", URL: "http://localhost:3100"}},
	}

	p, err := New(logger, cfg)
	require.NoError(t, err)

	mux := NewServeMux(logger, p, func() error {
		return errors.New("bad config file")
	}, true)

	req := httptest.NewRequest(http.MethodPost, "/-/reload", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	require.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "bad config file")
}

func TestNewServeMux_Reload_LifecycleDisabled(t *testing.T) {
	logger := log.NewNopLogger()
	cfg := &config.Config{
		ServerGroups: []config.ServerGroup{{Name: "loki1", URL: "http://localhost:3100"}},
	}

	p, err := New(logger, cfg)
	require.NoError(t, err)

	called := false
	mux := NewServeMux(logger, p, func() error {
		called = true
		return nil
	}, false)

	req := httptest.NewRequest(http.MethodPost, "/-/reload", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	require.Equal(t, http.StatusForbidden, w.Code)
	assert.False(t, called)
}

func TestNewServeMux_Reload_MethodNotAllowed(t *testing.T) {
	logger := log.NewNopLogger()
	cfg := &config.Config{
		ServerGroups: []config.ServerGroup{{Name: "loki1", URL: "http://localhost:3100"}},
	}

	p, err := New(logger, cfg)
	require.NoError(t, err)

	called := false
	mux := NewServeMux(logger, p, func() error {
		called = true
		return nil
	}, true)

	// A GET must not be proxied to the backends nor trigger a reload.
	req := httptest.NewRequest(http.MethodGet, "/-/reload", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	require.Equal(t, http.StatusMethodNotAllowed, w.Code)
	assert.Equal(t, "POST, PUT", w.Header().Get("Allow"))
	assert.False(t, called)
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

	mux := mustMux(t, logger, cfg)

	// Hit an unknown path that falls through to the catch-all proxy handler
	req := httptest.NewRequest(http.MethodGet, "/some/unknown/path", nil)
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)

	// The catch-all uses forwardFirstResponse which forwards the upstream's response
	require.Equal(t, http.StatusOK, w.Code)
}
