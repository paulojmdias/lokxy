package handler

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/gorilla/websocket"
	cfg "github.com/paulojmdias/lokxy/pkg/config"
)

// WebSocket upgrader
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(_ *http.Request) bool {
		return true
	},
}

// Create WebSocket dialer with TLS config
func createWebSocketDialer(instance cfg.ServerGroup, logger log.Logger) (*websocket.Dialer, error) {
	tlsConfig := &tls.Config{
		InsecureSkipVerify: instance.HTTPClientConfig.TLSConfig.InsecureSkipVerify,
	}

	if instance.HTTPClientConfig.TLSConfig.CAFile != "" {
		caCert, err := os.ReadFile(instance.HTTPClientConfig.TLSConfig.CAFile)
		if err != nil {
			level.Error(logger).Log("msg", "Failed to load CA certificate", "err", err)
			return nil, err
		}
		caCertPool := x509.NewCertPool()
		caCertPool.AppendCertsFromPEM(caCert)
		tlsConfig.RootCAs = caCertPool
	}

	if instance.HTTPClientConfig.TLSConfig.CertFile != "" && instance.HTTPClientConfig.TLSConfig.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(instance.HTTPClientConfig.TLSConfig.CertFile, instance.HTTPClientConfig.TLSConfig.KeyFile)
		if err != nil {
			level.Error(logger).Log("msg", "Failed to load client cert", "err", err)
			return nil, err
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}

	return &websocket.Dialer{
		Proxy:            http.ProxyFromEnvironment,
		HandshakeTimeout: instance.HTTPClientConfig.DialTimeout,
		TLSClientConfig:  tlsConfig,
	}, nil
}

// HandleTailWebSocket proxies Loki's /tail endpoint
func HandleTailWebSocket(w http.ResponseWriter, r *http.Request, config *cfg.Config, logger log.Logger) {
	// Upgrade client connection
	clientConn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		level.Error(logger).Log("msg", "Failed to upgrade client WS", "err", err)
		http.Error(w, `{"status":"error","message":"failed to upgrade WebSocket connection"}`, http.StatusBadRequest)
		return
	}
	defer clientConn.Close()

	mergedResponses := make(chan map[string]any)
	var wg sync.WaitGroup
	var connectedBackend int
	var mu sync.Mutex

	for _, instance := range config.ServerGroups {
		wg.Add(1)
		go func(instance cfg.ServerGroup) {
			defer wg.Done()

			targetURL := instance.URL
			if after, ok := strings.CutPrefix(targetURL, "http://"); ok {
				targetURL = "ws://" + after
			} else if after, ok := strings.CutPrefix(targetURL, "https://"); ok {
				targetURL = "wss://" + after
			}
			targetURL += r.URL.Path
			if r.URL.RawQuery != "" {
				targetURL += "?" + r.URL.RawQuery
			}

			dialer, err := createWebSocketDialer(instance, logger)
			if err != nil {
				level.Error(logger).Log("msg", "Failed to create dialer", "err", err)
				return
			}

			headers := http.Header{}
			for k, v := range instance.Headers {
				headers.Set(k, v)
			}

			backendConn, resp, err := dialer.Dial(targetURL, headers)
			if err != nil {
				level.Error(logger).Log("msg", "Failed to connect upstream Loki WS", "url", targetURL, "err", err)
				if resp != nil {
					// Forward Loki’s error response directly
					body, _ := io.ReadAll(resp.Body)
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(resp.StatusCode)
					_, _ = w.Write(body)
				}
				return
			}
			defer backendConn.Close()

			mu.Lock()
			connectedBackend++
			mu.Unlock()

			for {
				_, msg, err := backendConn.ReadMessage()
				if err != nil {
					level.Error(logger).Log("msg", "Failed to read from upstream WS", "err", err)
					return
				}

				var decoded map[string]any
				if err := json.Unmarshal(msg, &decoded); err != nil {
					level.Error(logger).Log("msg", "Failed to decode upstream WS message", "err", err)
					return
				}

				mergedResponses <- decoded
			}
		}(instance)
	}

	// Forward merged messages to client
	go func() {
		for resp := range mergedResponses {
			if err := clientConn.WriteJSON(resp); err != nil {
				level.Error(logger).Log("msg", "Failed to write to client WS", "err", err)
				return
			}
		}
	}()

	wg.Wait()
	close(mergedResponses)

	mu.Lock()
	level.Info(logger).Log("msg", "WebSocket tail completed", "connected_backends", connectedBackend)
	mu.Unlock()
}
