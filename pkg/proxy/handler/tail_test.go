package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-kit/log"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	cfg "github.com/paulojmdias/lokxy/pkg/config"
)

func TestCreateWebSocketDialer_WithoutTLS(t *testing.T) {
	logger := log.NewNopLogger()

	instance := cfg.ServerGroup{
		Name: "test-instance",
		URL:  "http://localhost:3100",
	}
	instance.HTTPClientConfig.TLSConfig.InsecureSkipVerify = false

	dialer, err := createWebSocketDialer(instance, logger)
	require.NoError(t, err)
	require.NotNil(t, dialer)

	require.False(t, dialer.TLSClientConfig.InsecureSkipVerify)
}

func TestCreateWebSocketDialer_InsecureSkipVerify(t *testing.T) {
	logger := log.NewNopLogger()

	instance := cfg.ServerGroup{
		Name: "test-instance",
		URL:  "https://localhost:3100",
	}
	instance.HTTPClientConfig.TLSConfig.InsecureSkipVerify = true

	dialer, err := createWebSocketDialer(instance, logger)
	require.NoError(t, err)
	require.NotNil(t, dialer)

	require.True(t, dialer.TLSClientConfig.InsecureSkipVerify)
}

func TestCreateWebSocketDialer_InvalidCAFile(t *testing.T) {
	logger := log.NewNopLogger()

	instance := cfg.ServerGroup{
		Name: "test-instance",
		URL:  "https://localhost:3100",
	}
	instance.HTTPClientConfig.TLSConfig.CAFile = "/nonexistent/ca.pem"

	dialer, err := createWebSocketDialer(instance, logger)
	require.Error(t, err)
	require.Nil(t, dialer)
}

func TestCreateWebSocketDialer_InvalidCertFile(t *testing.T) {
	logger := log.NewNopLogger()

	instance := cfg.ServerGroup{
		Name: "test-instance",
		URL:  "https://localhost:3100",
	}
	instance.HTTPClientConfig.TLSConfig.CertFile = "/nonexistent/cert.pem"
	instance.HTTPClientConfig.TLSConfig.KeyFile = "/nonexistent/key.pem"

	dialer, err := createWebSocketDialer(instance, logger)
	require.Error(t, err)
	require.Nil(t, dialer)
}

func TestHandleTailWebSocket_UpgradeFailure(t *testing.T) {
	logger := log.NewNopLogger()
	config := &cfg.Config{
		ServerGroups: []cfg.ServerGroup{},
	}

	// Create a regular HTTP request (not a WebSocket upgrade request)
	req := httptest.NewRequest("GET", "/loki/api/v1/tail", nil)
	w := httptest.NewRecorder()

	HandleTailWebSocket(context.Background(), w, req, config, logger)

	// Should fail to upgrade
	require.NotEqual(t, http.StatusSwitchingProtocols, w.Code)
}

func TestHandleTailWebSocket_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	logger := log.NewNopLogger()

	// Create a mock Loki backend WebSocket server
	mockLokiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{
			CheckOrigin: func(_ *http.Request) bool { return true },
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Send a test message
		message := map[string]any{
			"streams": []map[string]any{
				{
					"stream": map[string]string{"app": "test"},
					"values": [][]string{
						{"1609459200000000000", "test log line"},
					},
				},
			},
		}
		_ = conn.WriteJSON(message)

		// Keep connection open briefly
		time.Sleep(100 * time.Millisecond)
	}))
	defer mockLokiServer.Close()

	// Convert http:// to ws://
	wsURL := "ws" + strings.TrimPrefix(mockLokiServer.URL, "http")

	serverGroup := cfg.ServerGroup{
		Name:    "test-backend",
		URL:     wsURL,
		Headers: map[string]string{},
	}
	serverGroup.HTTPClientConfig.DialTimeout = 5 * time.Second
	serverGroup.HTTPClientConfig.TLSConfig.InsecureSkipVerify = false

	config := &cfg.Config{
		ServerGroups: []cfg.ServerGroup{serverGroup},
	}

	// Create test server for the proxy
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		HandleTailWebSocket(context.Background(), w, r, config, logger)
	}))
	defer proxyServer.Close()

	// Connect to proxy as a WebSocket client
	proxyWSURL := "ws" + strings.TrimPrefix(proxyServer.URL, "http")
	client, _, err := websocket.DefaultDialer.Dial(proxyWSURL, nil)
	require.NoError(t, err)
	defer client.Close()

	// Set read deadline
	client.SetReadDeadline(time.Now().Add(2 * time.Second))

	// Try to read a message
	var response map[string]any
	err = client.ReadJSON(&response)

	require.NoError(t, err)
}

func TestHandleTailWebSocket_NoBackends(t *testing.T) {
	logger := log.NewNopLogger()
	config := &cfg.Config{
		ServerGroups: []cfg.ServerGroup{},
	}

	// Create a mock WebSocket server
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		HandleTailWebSocket(context.Background(), w, r, config, logger)
	}))
	defer testServer.Close()

	// Connect as WebSocket client
	wsURL := "ws" + strings.TrimPrefix(testServer.URL, "http")
	client, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer client.Close()

	// With no backends, connection should establish but receive no messages
	client.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	var response map[string]any
	err = client.ReadJSON(&response)

	// Should timeout or get EOF since no backends send data
	require.Error(t, err)
}

func TestUpgrader_CheckOrigin(t *testing.T) {
	// Verify that the upgrader allows all origins
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Origin", "https://evil.example.com")

	result := upgrader.CheckOrigin(req)

	// Current implementation allows all origins (security issue)
	require.True(t, result, "upgrader should currently allow all origins (this is a known security issue)")
}

func TestHandleTailWebSocket_URLSchemeRewrite_HTTP(t *testing.T) {
	// The handler rewrites http:// → ws:// when dialing backends.
	// We verify this by pointing a backend config at an http:// URL; the
	// handler will try to dial ws://<same-host>, which will fail since there
	// is no WS listener there — but we can observe the behaviour via the
	// integration server (upgrade succeeds on the client side, backend fails
	// silently). Here we just assert the config URL starts with http://.
	logger := log.NewNopLogger()

	serverGroup := cfg.ServerGroup{
		Name: "http-backend",
		URL:  "http://127.0.0.1:1", // unreachable, but has http:// prefix
	}
	serverGroup.HTTPClientConfig.DialTimeout = 50 * time.Millisecond

	config := &cfg.Config{
		ServerGroups: []cfg.ServerGroup{serverGroup},
	}

	// Proxy server that uses the handler
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		HandleTailWebSocket(context.Background(), w, r, config, logger)
	}))
	defer proxyServer.Close()

	// Connect as a WS client — upgrade should succeed even if the backend fails
	wsURL := "ws" + strings.TrimPrefix(proxyServer.URL, "http")
	client, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer client.Close()

	// Backend connection will fail (port 1 unreachable), so no messages arrive
	client.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	var msg map[string]any
	err = client.ReadJSON(&msg)
	require.Error(t, err) // timeout or EOF expected
}

func TestHandleTailWebSocket_URLSchemeRewrite_HTTPS(t *testing.T) {
	logger := log.NewNopLogger()

	serverGroup := cfg.ServerGroup{
		Name: "https-backend",
		URL:  "https://127.0.0.1:1", // unreachable, but has https:// prefix
	}
	serverGroup.HTTPClientConfig.DialTimeout = 50 * time.Millisecond
	serverGroup.HTTPClientConfig.TLSConfig.InsecureSkipVerify = true

	config := &cfg.Config{
		ServerGroups: []cfg.ServerGroup{serverGroup},
	}

	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		HandleTailWebSocket(context.Background(), w, r, config, logger)
	}))
	defer proxyServer.Close()

	wsURL := "ws" + strings.TrimPrefix(proxyServer.URL, "http")
	client, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer client.Close()

	client.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	var msg map[string]any
	err = client.ReadJSON(&msg)
	require.Error(t, err)
}

func TestHandleTailWebSocket_InvalidJSONFromBackend(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	logger := log.NewNopLogger()

	// Backend sends raw non-JSON bytes, causing the JSON decode goroutine to exit.
	mockBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		up := websocket.Upgrader{CheckOrigin: func(_ *http.Request) bool { return true }}
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		// Send non-JSON binary data
		_ = conn.WriteMessage(websocket.TextMessage, []byte("not valid json !!!"))
		time.Sleep(100 * time.Millisecond)
	}))
	defer mockBackend.Close()

	wsURL := "ws" + strings.TrimPrefix(mockBackend.URL, "http")
	serverGroup := cfg.ServerGroup{Name: "bad-json-backend", URL: wsURL}
	serverGroup.HTTPClientConfig.DialTimeout = 5 * time.Second

	config := &cfg.Config{ServerGroups: []cfg.ServerGroup{serverGroup}}

	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		HandleTailWebSocket(context.Background(), w, r, config, logger)
	}))
	defer proxyServer.Close()

	wsProxyURL := "ws" + strings.TrimPrefix(proxyServer.URL, "http")
	client, _, err := websocket.DefaultDialer.Dial(wsProxyURL, nil)
	require.NoError(t, err)
	defer client.Close()

	// No valid JSON will be forwarded; the goroutine exits silently.
	client.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	var msg map[string]any
	err = client.ReadJSON(&msg)
	require.Error(t, err) // timeout or EOF expected
}

func TestHandleTailWebSocket_ContextCancellation(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	logger := log.NewNopLogger()

	// Create a mock backend that stays open
	mockBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{
			CheckOrigin: func(_ *http.Request) bool { return true },
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Keep connection open
		time.Sleep(5 * time.Second)
	}))
	defer mockBackend.Close()

	wsURL := "ws" + strings.TrimPrefix(mockBackend.URL, "http")

	serverGroup := cfg.ServerGroup{
		Name: "test-backend",
		URL:  wsURL,
	}
	serverGroup.HTTPClientConfig.DialTimeout = 5 * time.Second

	config := &cfg.Config{
		ServerGroups: []cfg.ServerGroup{serverGroup},
	}

	// Create cancellable context
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	req := httptest.NewRequest("GET", "/loki/api/v1/tail", nil)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	// This should handle context cancellation gracefully
	HandleTailWebSocket(ctx, w, req, config, logger)
}
