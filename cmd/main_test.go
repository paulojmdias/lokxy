package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	"github.com/paulojmdias/lokxy/pkg/config"
	"github.com/paulojmdias/lokxy/pkg/o11y/logging"
)

func TestMainFunction(t *testing.T) {
	// Set up a temporary config file
	configContent := `
logging:
  level: info
  format: json
server_groups:
  - name: loki1
    url: http://loki1.example.com
    timeout: 10
    headers:
      Authorization: Bearer token1
  - name: loki2
    url: http://loki2.example.com
    timeout: 15
    headers:
      Authorization: Bearer token2
`
	configFile, err := os.CreateTemp("", "config.yaml")
	if err != nil {
		t.Fatalf("Failed to create temp config file: %v", err)
	}
	defer os.Remove(configFile.Name())

	if _, err := configFile.Write([]byte(configContent)); err != nil {
		t.Fatalf("Failed to write to temp config file: %v", err)
	}
	configFile.Close()

	// Load configuration
	cfg, err := config.LoadConfig(configFile.Name())
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	logger := logging.ConfigureLogger(cfg.Logging)

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	eg, ctx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		return run(ctx, logger, cfg, runOptions{
			bindAddr:        ":3100",
			metricsAddr:     ":9091",
			configPath:      configFile.Name(),
			enableLifecycle: true,
		})
	})

	require.Eventuallyf(t, func() bool {
		resp, err := http.Get("http://localhost:9091/metrics")
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 5*time.Second, 50*time.Millisecond, "endpoint %s was not ready within 5s", "http://localhost:9091/metrics")

	require.Eventuallyf(t, func() bool {
		resp, err := http.Get("http://localhost:3100/healthy")
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 5*time.Second, 50*time.Millisecond, "endpoint %s was not ready within 5s", "http://localhost:3100/healthy")

	// Test cases
	t.Run("test metrics endpoint", func(t *testing.T) {
		resp, err := http.Get("http://localhost:9091/metrics")
		if err != nil {
			t.Fatalf("Failed to get metrics: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("Handler returned wrong status code: got %v want %v", resp.StatusCode, http.StatusOK)
		}
	})

	t.Run("test proxy handler after reload", func(t *testing.T) {
		backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer backend.Close()

		// Point the config file at the test backend and reload via the
		// lifecycle endpoint. Otherwise unreachable backends cause 502 errors.
		newConfig := `
logging:
  level: info
  format: json
server_groups:
  - name: loki1
    url: ` + backend.URL + `
    timeout: 10
`
		require.NoError(t, os.WriteFile(configFile.Name(), []byte(newConfig), 0o600))

		reloadResp, err := http.Post("http://localhost:3100/-/reload", "", nil)
		require.NoError(t, err)
		defer reloadResp.Body.Close()
		require.Equal(t, http.StatusOK, reloadResp.StatusCode)

		req, err := http.NewRequest("GET", "http://localhost:3100/", nil)
		if err != nil {
			t.Fatalf("Failed to create request: %v", err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Failed to make request: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("Handler returned wrong status code: got %v want %v", resp.StatusCode, http.StatusOK)
		}
	})

	t.Run("test reload with invalid config fails and keeps serving", func(t *testing.T) {
		// Break the config file: reload must fail with 500 and the proxy must
		// keep the previously loaded configuration.
		require.NoError(t, os.WriteFile(configFile.Name(), []byte("server_groups: []\n"), 0o600))

		reloadResp, err := http.Post("http://localhost:3100/-/reload", "", nil)
		require.NoError(t, err)
		defer reloadResp.Body.Close()
		require.Equal(t, http.StatusInternalServerError, reloadResp.StatusCode)

		resp, err := http.Get("http://localhost:3100/healthy")
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("test reload metrics exposed", func(t *testing.T) {
		resp, err := http.Get("http://localhost:9091/metrics")
		require.NoError(t, err)
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Contains(t, string(body), "lokxy_config_last_reload_successful")
	})

	t.Run("test healthy endpoint", func(t *testing.T) {
		resp, err := http.Get("http://localhost:3100/healthy")
		if err != nil {
			t.Fatalf("Failed to get healthy: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("Handler returned wrong status code: got %v want %v", resp.StatusCode, http.StatusOK)
		}
	})

	t.Run("test readiness before ready", func(t *testing.T) {
		config.SetReady(false)
		resp, err := http.Get("http://localhost:3100/ready")
		if err != nil {
			t.Fatalf("Failed to get readiness: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Errorf("Expected not ready status, got: %v", resp.StatusCode)
		}
	})

	t.Run("test readiness after ready", func(t *testing.T) {
		config.SetReady(true)
		resp, err := http.Get("http://localhost:3100/ready")
		if err != nil {
			t.Fatalf("Failed to get readiness: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected ready status, got: %v", resp.StatusCode)
		}
	})
	cancel()
	err = eg.Wait()
	require.NoError(t, err)
}

func TestRun_ListenerBindFailure(t *testing.T) {
	// Occupy a port so the second bind attempt fails.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()
	occupiedAddr := ln.Addr().String()

	cfg, err := config.LoadConfig(writeTempConfig(t))
	require.NoError(t, err)
	logger := logging.ConfigureLogger(cfg.Logging)

	ctx := t.Context()
	// Pass the already-occupied address as the metrics bind address.
	err = run(ctx, logger, cfg, runOptions{bindAddr: "127.0.0.1:0", metricsAddr: occupiedAddr})
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to start")
}

func TestRun_ContextCancellation(t *testing.T) {
	cfg, err := config.LoadConfig(writeTempConfig(t))
	require.NoError(t, err)
	logger := logging.ConfigureLogger(cfg.Logging)

	ctx, cancel := context.WithCancel(t.Context())

	done := make(chan error, 1)
	go func() {
		done <- run(ctx, logger, cfg, runOptions{bindAddr: "127.0.0.1:0", metricsAddr: "127.0.0.1:0"})
	}()

	// Let the servers start, then cancel.
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("run() did not return after context cancellation")
	}
}

// writeTempConfig writes a minimal valid config file and returns its path.
func writeTempConfig(t *testing.T) string {
	t.Helper()
	f, err := os.CreateTemp("", "lokxy-test-*.yaml")
	require.NoError(t, err)
	t.Cleanup(func() { os.Remove(f.Name()) })

	_, err = f.WriteString(`
logging:
  level: info
  format: json
server_groups:
  - name: loki1
    url: http://localhost:3100
    timeout: 1
`)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	return f.Name()
}
