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
	traces "github.com/paulojmdias/lokxy/pkg/o11y/tracing"
	proxyErrors "github.com/paulojmdias/lokxy/pkg/proxy/errors"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// WebSocket upgrader
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(_ *http.Request) bool {
		return true // allow all origins
	},
}

// createWebSocketDialer builds a dialer with TLS if needed
func createWebSocketDialer(instance cfg.ServerGroup, logger log.Logger) (*websocket.Dialer, error) {
	tlsConfig := &tls.Config{
		InsecureSkipVerify: instance.HTTPClientConfig.TLSConfig.InsecureSkipVerify,
	}

	// Load CA cert
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

	// Load client certs
	if instance.HTTPClientConfig.TLSConfig.CertFile != "" && instance.HTTPClientConfig.TLSConfig.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(instance.HTTPClientConfig.TLSConfig.CertFile, instance.HTTPClientConfig.TLSConfig.KeyFile)
		if err != nil {
			level.Error(logger).Log("msg", "Failed to load client certs", "err", err)
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

// HandleTailWebSocket upgrades the client connection and streams logs
func HandleTailWebSocket(w http.ResponseWriter, r *http.Request, config *cfg.Config, logger log.Logger) {
	ctx := r.Context()
	ctx, span := traces.CreateSpan(ctx, "websocket_tail_handler")
	defer span.End()

	span.SetAttributes(attribute.Int("server_groups", len(config.ServerGroups)))

	// unwrap ResponseWriter if wrapped by traces.go
	realWriter := w
	if uw, ok := w.(interface{ Unwrap() http.ResponseWriter }); ok {
		realWriter = uw.Unwrap()
	}

	// Upgrade client connection
	clientConn, err := upgrader.Upgrade(realWriter, r, nil)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "Failed to upgrade WebSocket")
		level.Error(logger).Log("msg", proxyErrors.ErrUpgradeFailed.Error(), "err", err)
		proxyErrors.WriteJSONError(w, http.StatusBadRequest, proxyErrors.ErrUpgradeFailed.Error())
		return
	}
	defer clientConn.Close()
	span.SetAttributes(attribute.Bool("websocket.upgraded", true))

	mergedResponses := make(chan map[string]any)
	var wg sync.WaitGroup
	var connectedBackend int
	var backendMutex sync.Mutex

	// loop through backends
	for _, instance := range config.ServerGroups {
		wg.Add(1)
		go func(instance cfg.ServerGroup) {
			defer wg.Done()
			_, backendSpan := traces.CreateSpan(ctx, "tail_upstream")
			defer backendSpan.End()

			// rewrite URL -> ws(s)://
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

			backendSpan.SetAttributes(attribute.String("upstream.url", targetURL))

			dialer, err := createWebSocketDialer(instance, logger)
			if err != nil {
				backendSpan.RecordError(err)
				level.Error(logger).Log("msg", "Dialer failed", "err", err)
				return
			}

			headers := http.Header{}
			for k, v := range instance.Headers {
				headers.Set(k, v)
			}

			conn, resp, err := dialer.Dial(targetURL, headers)
			if err != nil {
				backendSpan.RecordError(err)
				backendSpan.SetStatus(codes.Error, "Failed to dial upstream")
				if resp != nil {
					body, _ := io.ReadAll(resp.Body)
					level.Error(logger).Log("msg", "Handshake failed", "status", resp.StatusCode, "body", string(body))
				}
				level.Error(logger).Log("msg", proxyErrors.ErrBackendDialFailed.Error(), "err", err)
				return
			}
			defer conn.Close()

			backendMutex.Lock()
			connectedBackend++
			backendMutex.Unlock()

			for {
				_, msg, err := conn.ReadMessage()
				if err != nil {
					backendSpan.RecordError(err)
					level.Error(logger).Log("msg", proxyErrors.ErrReadMessageFailed.Error(), "err", err)
					return
				}
				var result map[string]any
				if err := json.Unmarshal(msg, &result); err != nil {
					backendSpan.RecordError(err)
					level.Error(logger).Log("msg", "JSON unmarshal failed", "err", err)
					return
				}
				mergedResponses <- result
			}
		}(instance)
	}

	// forward to client
	go func() {
		for resp := range mergedResponses {
			if err := clientConn.WriteJSON(resp); err != nil {
				level.Error(logger).Log("msg", proxyErrors.ErrWriteMessageFailed.Error(), "err", err)
				return
			}
		}
	}()

	wg.Wait()
	close(mergedResponses)

	backendMutex.Lock()
	span.SetAttributes(attribute.Int("upstreams.connected", connectedBackend))
	backendMutex.Unlock()
}
