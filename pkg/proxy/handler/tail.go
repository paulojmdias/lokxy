package handler

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"io/ioutil"
	"net/http"
	"strings"
	"sync"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/gorilla/websocket"

	cfg "github.com/paulojmdias/lokxy/pkg/config"
	"github.com/paulojmdias/lokxy/pkg/o11y/metrics"
)

// WebSocket upgrader to upgrade the HTTP connection to a WebSocket connection
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins
	},
}

// createWebSocketDialer creates a WebSocket dialer with the appropriate TLS configuration
func createWebSocketDialer(instance cfg.ServerGroup, logger log.Logger) (*websocket.Dialer, error) {
	tlsConfig := &tls.Config{
		InsecureSkipVerify: instance.HTTPClientConfig.TLSConfig.InsecureSkipVerify,
	}

	// Load CA certificate if provided
	if instance.HTTPClientConfig.TLSConfig.CAFile != "" {
		caCert, err := ioutil.ReadFile(instance.HTTPClientConfig.TLSConfig.CAFile)
		if err != nil {
			level.Error(logger).Log("msg", "Failed to load CA certificate", "file", instance.HTTPClientConfig.TLSConfig.CAFile, "err", err)
			return nil, err
		}
		caCertPool := x509.NewCertPool()
		caCertPool.AppendCertsFromPEM(caCert)
		tlsConfig.RootCAs = caCertPool
	}

	// Load client certificate for mutual TLS if provided
	if instance.HTTPClientConfig.TLSConfig.CertFile != "" && instance.HTTPClientConfig.TLSConfig.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(instance.HTTPClientConfig.TLSConfig.CertFile, instance.HTTPClientConfig.TLSConfig.KeyFile)
		if err != nil {
			level.Error(logger).Log("msg", "Failed to load client certificate and key", "certFile", instance.HTTPClientConfig.TLSConfig.CertFile, "keyFile", instance.HTTPClientConfig.TLSConfig.KeyFile, "err", err)
			return nil, err
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}

	dialer := &websocket.Dialer{
		Proxy:            http.ProxyFromEnvironment,
		HandshakeTimeout: instance.HTTPClientConfig.DialTimeout,
		TLSClientConfig:  tlsConfig,
	}

	return dialer, nil
}

// Handle WebSocket connections for the Loki Tail API
func HandleTailWebSocket(w http.ResponseWriter, r *http.Request, config *cfg.Config, logger log.Logger) {
	// Upgrade the HTTP connection to a WebSocket connection
	clientConn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		level.Error(logger).Log("msg", "Failed to upgrade connection", "err", err)
		return
	}
	defer clientConn.Close()

	// Create a WaitGroup to handle multiple WebSocket connections
	var wg sync.WaitGroup
	mergedResponses := make(chan map[string]interface{})

	// Loop through each Loki instance and create WebSocket connections
	for _, instance := range config.ServerGroups {
		wg.Add(1)

		go func(instance cfg.ServerGroup) {
			defer wg.Done()

			// Build the WebSocket target URL
			targetURL := instance.URL
			if strings.HasPrefix(targetURL, "http://") {
				targetURL = "ws://" + strings.TrimPrefix(targetURL, "http://")
			} else if strings.HasPrefix(targetURL, "https://") {
				targetURL = "wss://" + strings.TrimPrefix(targetURL, "https://")
			}

			targetURL += r.URL.Path
			if r.URL.RawQuery != "" {
				targetURL += "?" + r.URL.RawQuery
			}

			level.Info(logger).Log("msg", "Connecting to Loki WebSocket instance", "url", targetURL)

			// Create WebSocket dialer with TLS config
			dialer, err := createWebSocketDialer(instance, logger)
			if err != nil {
				metrics.RequestFailures.WithLabelValues("/loki/api/v1/tail", "GET", instance.Name).Inc() // Record error count
				level.Error(logger).Log("msg", "Failed to create WebSocket dialer", "instance", instance.Name, "err", err)
				return
			}

			// Record the request
			metrics.RequestCount.WithLabelValues("/loki/api/v1/tail", "GET", instance.Name).Inc()

			// Create WebSocket connection to Loki instance
			headers := http.Header{}
			for key, value := range instance.Headers {
				headers.Set(key, value)
			}

			backendConn, resp, err := dialer.Dial(targetURL, headers)
			if err != nil {
				metrics.RequestFailures.WithLabelValues("/loki/api/v1/tail", "GET", instance.Name).Inc() // Record error count
				level.Error(logger).Log("msg", "Failed to connect to Loki WebSocket", "instance", instance.Name, "err", err)
				if resp != nil {
					metrics.RequestFailures.WithLabelValues("/loki/api/v1/tail", "GET", instance.Name).Inc() // Record error count
					body, _ := io.ReadAll(resp.Body)
					level.Error(logger).Log("msg", "Handshake response", "status", resp.StatusCode, "body", string(body))
				}
				return
			}
			defer backendConn.Close()

			// Listen for messages from Loki instance WebSocket
			for {
				_, message, err := backendConn.ReadMessage()
				if err != nil {
					metrics.RequestFailures.WithLabelValues("/loki/api/v1/tail", "GET", instance.Name).Inc() // Record error count
					level.Error(logger).Log("msg", "Error reading WebSocket message", "instance", instance.Name, "err", err)
					return
				}

				// Parse and forward the message to the merged response channel
				var result map[string]interface{}
				if err := json.Unmarshal(message, &result); err != nil {
					metrics.RequestFailures.WithLabelValues("/loki/api/v1/tail", "GET", instance.Name).Inc() // Record error count
					level.Error(logger).Log("msg", "Failed to decode WebSocket message", "instance", instance.Name, "err", err)
					return
				}

				mergedResponses <- result
			}
		}(instance)
	}

	// Goroutine to forward merged responses to the client WebSocket
	go func() {
		for response := range mergedResponses {
			if err := clientConn.WriteJSON(response); err != nil {
				level.Error(logger).Log("msg", "Error writing to client WebSocket", "err", err)
				return
			}
		}
	}()

	// Wait for all goroutines to finish
	wg.Wait()
	close(mergedResponses)
}
