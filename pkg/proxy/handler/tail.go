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
	"github.com/paulojmdias/lokxy/pkg/o11y/metrics"
	traces "github.com/paulojmdias/lokxy/pkg/o11y/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
)

// WebSocket upgrader to upgrade the HTTP connection to a WebSocket connection
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(_ *http.Request) bool {
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
		caCert, err := os.ReadFile(instance.HTTPClientConfig.TLSConfig.CAFile)
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
	ctx := r.Context()
	ctx, span := traces.CreateSpan(ctx, "websocket_tail_handler")
	defer span.End()

	span.SetAttributes(
		attribute.Int("server_groups", len(config.ServerGroups)),
	)

	// Upgrade the HTTP connection to a WebSocket connection
	clientConn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "Failed to upgrade connection")
		level.Error(logger).Log("msg", "Failed to upgrade connection", "err", err)
		return
	}
	defer clientConn.Close()

	span.SetAttributes(attribute.Bool("websocket.upgraded", true))

	// Create a WaitGroup to handle multiple WebSocket connections
	var wg sync.WaitGroup
	mergedResponses := make(chan map[string]any)

	var connectedBackend int
	var backendMutex sync.Mutex

	// Loop through each Loki instance and create WebSocket connections
	for _, instance := range config.ServerGroups {
		wg.Add(1)

		go func(instance cfg.ServerGroup) {
			defer wg.Done()

			upstreamCtx, backendSpan := traces.CreateSpan(ctx, "websocket_backend_connection")
			defer backendSpan.End()

			backendSpan.SetAttributes(
				attribute.String("upstream.name", instance.Name),
				attribute.String("upstream.base_url", instance.URL),
			)

			// Build the WebSocket target URL
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

			backendSpan.SetAttributes(attribute.String("upstream.target_url", targetURL))
			level.Info(logger).Log("msg", "Connecting to Loki WebSocket instance", "url", targetURL)

			// Create WebSocket dialer with TLS config
			dialer, err := createWebSocketDialer(instance, logger)
			if err != nil {
				backendSpan.RecordError(err)
				backendSpan.SetStatus(codes.Error, "Failed to create WebSocket dialer")
				metrics.RequestFailures.Add(upstreamCtx, 1, metric.WithAttributes(
					attribute.String("path", "/loki/api/v1/tail"),
					attribute.String("method", "GET"),
					attribute.String("instance", instance.Name),
				))
				level.Error(logger).Log("msg", "Failed to create WebSocket dialer", "instance", instance.Name, "err", err)
				return
			}

			// Record the request
			metrics.RequestCount.Add(upstreamCtx, 1, metric.WithAttributes(
				attribute.String("path", "/loki/api/v1/tail"),
				attribute.String("method", "GET"),
				attribute.String("instance", instance.Name),
			))

			// Create WebSocket connection to Loki instance
			headers := http.Header{}
			for key, value := range instance.Headers {
				headers.Set(key, value)
			}

			traces.InjectTraceToHTTPRequest(upstreamCtx, &http.Request{Header: headers})

			backendConn, resp, err := dialer.Dial(targetURL, headers)
			if err != nil {
				backendSpan.RecordError(err)
				backendSpan.SetStatus(codes.Error, "Failed to connect to Loki WebSocket")
				// Record error count
				metrics.RequestFailures.Add(upstreamCtx, 1, metric.WithAttributes(
					attribute.String("path", "/loki/api/v1/tail"),
					attribute.String("method", "GET"),
					attribute.String("instance", instance.Name),
				))
				level.Error(logger).Log("msg", "Failed to connect to Loki WebSocket", "instance", instance.Name, "err", err)
				if resp != nil {
					backendSpan.SetAttributes(attribute.Int("upstream.handshake_status", resp.StatusCode))
					// Record error count
					metrics.RequestFailures.Add(upstreamCtx, 1, metric.WithAttributes(
						attribute.String("path", "/loki/api/v1/tail"),
						attribute.String("method", "GET"),
						attribute.String("instance", instance.Name),
					))
					body, _ := io.ReadAll(resp.Body)
					level.Error(logger).Log("msg", "Handshake response", "status", resp.StatusCode, "body", string(body))
				}
				return
			}
			defer backendConn.Close()

			backendMutex.Lock()
			connectedBackend++
			backendMutex.Unlock()

			backendSpan.SetAttributes(
				attribute.Bool("upstream.connected", true),
				attribute.Int("upstream.handshake_status", 101),
			)

			messageCount := 0

			// Listen for messages from Loki instance WebSocket
			for {
				_, message, err := backendConn.ReadMessage()
				if err != nil {
					backendSpan.RecordError(err)
					backendSpan.SetStatus(codes.Error, "Error reading WebSocket message")
					metrics.RequestFailures.Add(upstreamCtx, 1, metric.WithAttributes(
						attribute.String("path", "/loki/api/v1/tail"),
						attribute.String("method", "GET"),
						attribute.String("instance", instance.Name),
					))
					level.Error(logger).Log("msg", "Error reading WebSocket message", "instance", instance.Name, "err", err)

					backendSpan.SetAttributes(attribute.Int("upstream.messages_received", messageCount))
					return
				}

				messageCount++

				// Parse and forward the message to the merged response channel
				var result map[string]any
				if err := json.Unmarshal(message, &result); err != nil {
					backendSpan.RecordError(err)
					// Record error count
					metrics.RequestFailures.Add(upstreamCtx, 1, metric.WithAttributes(
						attribute.String("path", "/loki/api/v1/tail"),
						attribute.String("method", "GET"),
						attribute.String("instance", instance.Name),
					))
					level.Error(logger).Log("msg", "Failed to decode WebSocket message", "instance", instance.Name, "err", err)
					backendSpan.SetAttributes(attribute.Int("upstream.messages_received", messageCount))
					return
				}

				mergedResponses <- result

				if messageCount%100 == 0 {
					backendSpan.SetAttributes(attribute.Int("upstream.messages_received", messageCount))
				}
			}
		}(instance)
	}

	_, forwardSpan := traces.CreateSpan(ctx, "websocket_client_forward")
	forwardedMessages := 0

	// Goroutine to forward merged responses to the client WebSocket
	go func() {
		defer forwardSpan.End()

		for response := range mergedResponses {
			if err := clientConn.WriteJSON(response); err != nil {
				forwardSpan.RecordError(err)
				forwardSpan.SetStatus(codes.Error, "Error writing to client WebSocket")
				level.Error(logger).Log("msg", "Error writing to client WebSocket", "err", err)
				forwardSpan.SetAttributes(attribute.Int("client.messages_forwarded", forwardedMessages))
				return
			}
			forwardedMessages++

			// Update forwarded count periodically
			if forwardedMessages%100 == 0 {
				forwardSpan.SetAttributes(attribute.Int("client.messages_forwarded", forwardedMessages))
			}
		}

		forwardSpan.SetAttributes(
			attribute.Int("client.messages_forwarded", forwardedMessages),
			attribute.Bool("client.forwarding_complete", true),
		)
	}()

	// Wait for all goroutines to finish
	wg.Wait()
	close(mergedResponses)

	backendMutex.Lock()
	span.SetAttributes(
		attribute.Int("websocket.connected_upstreams", connectedBackend),
		attribute.Bool("websocket.completed", true),
	)
	backendMutex.Unlock()
}
