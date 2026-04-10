package proxy

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"time"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/errgroup"

	cfg "github.com/paulojmdias/lokxy/pkg/config"
	"github.com/paulojmdias/lokxy/pkg/o11y/metrics"
	traces "github.com/paulojmdias/lokxy/pkg/o11y/tracing"
	"github.com/paulojmdias/lokxy/pkg/proxy/handler"
	"github.com/paulojmdias/lokxy/pkg/proxy/proxyresponse"
)

// CustomRoundTripper intercepts the request and response
type CustomRoundTripper struct {
	rt     http.RoundTripper
	logger log.Logger
}

// redactHeaders returns a copy of the provided headers with sensitive values redacted.
func redactHeaders(h http.Header) http.Header {
	redacted := make(http.Header, len(h))

	for name, values := range h {
		nameLower := strings.ToLower(name)

		// Explicitly sensitive headers and simple heuristics for secrets.
		if nameLower == "authorization" ||
			nameLower == "proxy-authorization" ||
			nameLower == "cookie" ||
			nameLower == "set-cookie" ||
			strings.Contains(nameLower, "token") ||
			strings.Contains(nameLower, "secret") ||
			strings.Contains(nameLower, "password") {
			redacted[name] = []string{"REDACTED"}
			continue
		}

		// Non-sensitive header: copy as-is.
		copied := make([]string, len(values))
		copy(copied, values)
		redacted[name] = copied
	}

	return redacted
}

// RoundTrip method allows us to inspect and modify requests/responses
func (c *CustomRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if ce := level.Debug(c.logger); ce != nil {
		headersJSON, err := json.Marshal(redactHeaders(req.Header))
		if err != nil {
			_ = level.Error(c.logger).Log("msg", "Failed to marshal headers for logging", "err", err)
		} else {
			_ = ce.Log("msg", "Custom RoundTrip", "url", req.URL.String(), "headers", string(headersJSON))
		}
	}

	// Perform the actual request
	resp, err := c.rt.RoundTrip(req)
	if err != nil {
		return nil, err
	}

	// Log and handle the response as needed
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gzReader, err := gzip.NewReader(resp.Body)
		if err != nil {
			resp.Body.Close()
			return nil, err
		}
		resp.Body = io.NopCloser(gzReader) // Prevent early closure
	}

	// Add any custom behavior for the response here, if needed
	return resp, nil
}

// Function to create an HTTP client dynamically
func createHTTPClient(instance cfg.ServerGroup, logger log.Logger) (*http.Client, error) {
	// Set default timeout
	dialTimeout := instance.HTTPClientConfig.DialTimeout
	if dialTimeout == 0 {
		dialTimeout = 200 * time.Millisecond // Default timeout
	}

	// Set up the TLS configuration if needed
	tlsConfig := &tls.Config{
		InsecureSkipVerify: instance.HTTPClientConfig.TLSConfig.InsecureSkipVerify,
	}

	// Load CA certificate if provided
	if instance.HTTPClientConfig.TLSConfig.CAFile != "" {
		caCert, err := os.ReadFile(instance.HTTPClientConfig.TLSConfig.CAFile)
		if err != nil {
			return nil, err
		}
		caCertPool := x509.NewCertPool()
		caCertPool.AppendCertsFromPEM(caCert)
		tlsConfig.RootCAs = caCertPool
	}

	// Load client certificate if provided
	if instance.HTTPClientConfig.TLSConfig.CertFile != "" && instance.HTTPClientConfig.TLSConfig.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(instance.HTTPClientConfig.TLSConfig.CertFile, instance.HTTPClientConfig.TLSConfig.KeyFile)
		if err != nil {
			return nil, err
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}

	// Create HTTP transport with the custom TLS configuration and dial timeout
	dialer := &net.Dialer{
		Timeout: dialTimeout,
	}

	// Apply transport settings from config, falling back to defaults.
	tc := instance.HTTPClientConfig.Transport

	maxIdleConns := tc.MaxIdleConns
	if maxIdleConns == 0 {
		maxIdleConns = 100
	}
	maxIdleConnsPerHost := tc.MaxIdleConnsPerHost
	if maxIdleConnsPerHost == 0 {
		maxIdleConnsPerHost = 20
	}
	idleConnTimeout := tc.IdleConnTimeout
	if idleConnTimeout == 0 {
		idleConnTimeout = 90 * time.Second
	}
	expectContinueTimeout := tc.ExpectContinueTimeout
	if expectContinueTimeout == 0 {
		expectContinueTimeout = 1 * time.Second
	}

	responseHeaderTimeout := tc.ResponseHeaderTimeout
	if responseHeaderTimeout == 0 {
		responseHeaderTimeout = time.Duration(instance.Timeout) * time.Second
	}

	forceHTTP2 := true
	if tc.ForceAttemptHTTP2 != nil {
		forceHTTP2 = *tc.ForceAttemptHTTP2
	}

	transport := &http.Transport{
		TLSClientConfig:       tlsConfig,
		DialContext:           dialer.DialContext,
		DisableKeepAlives:     tc.DisableKeepAlives,
		MaxIdleConns:          maxIdleConns,
		MaxIdleConnsPerHost:   maxIdleConnsPerHost,
		IdleConnTimeout:       idleConnTimeout,
		ExpectContinueTimeout: expectContinueTimeout,
		ResponseHeaderTimeout: responseHeaderTimeout,
		ForceAttemptHTTP2:     forceHTTP2,
	}

	client := &http.Client{
		Timeout:   time.Duration(instance.Timeout) * time.Second,
		Transport: &CustomRoundTripper{rt: transport, logger: logger},
	}

	return client, nil
}

type (
	proxy struct {
		config  *cfg.Config
		logger  log.Logger
		clients map[string]*http.Client
	}
	transformFn func(context.Context, http.ResponseWriter, <-chan *proxyresponse.BackendResponse, log.Logger)
)

func proxyHandler(config *cfg.Config, logger log.Logger) func(http.ResponseWriter, *http.Request) {
	clients := make(map[string]*http.Client)
	for _, instance := range config.ServerGroups {
		client, err := createHTTPClient(instance, logger)
		if err != nil {
			level.Error(logger).Log("msg", "Failed to create HTTP client", "instance", instance.Name, "err", err)
			continue
		}
		clients[instance.Name] = client
	}

	p := &proxy{
		config:  config,
		logger:  logger,
		clients: clients,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/loki/api/v1/tail", func(w http.ResponseWriter, r *http.Request) {
		span := trace.SpanFromContext(r.Context())
		span.SetAttributes(attribute.String("proxy.route_type", "websocket"))
		handler.HandleTailWebSocket(r.Context(), w, r, config, logger)
	})

	mux.HandleFunc("/loki/api/v1/label/{name}/values", func(w http.ResponseWriter, r *http.Request) {
		span := trace.SpanFromContext(r.Context())
		span.SetAttributes(attribute.String("proxy.route_type", "label_values"))
		labelName := r.PathValue("name")
		incomingQuery := r.URL.Query().Get("query")
		if config.MetadataAsLabels.Enabled && incomingQuery != "" && incomingQuery != "{}" && isFieldExposed(labelName, config.MetadataAsLabels.Fields) {
			ctx := r.Context()
			overridePath := "/loki/api/v1/detected_field/" + labelName + "/values"
			fieldValuesCh := p.backgroundFanoutBytes(ctx, r, overridePath,
				func(c context.Context, w http.ResponseWriter,
					results <-chan *proxyresponse.BackendResponse, logger log.Logger,
				) {
					handler.HandleLokiDetectedFieldValues(c, w, results, labelName, logger)
				})
			p.fanoutRequest(w, r, func(c context.Context, w http.ResponseWriter,
				results <-chan *proxyresponse.BackendResponse, logger log.Logger,
			) {
				handler.HandleLokiLabelValuesWithMetadataField(c, w, results, <-fieldValuesCh, logger)
			})
		} else {
			p.fanoutRequest(w, r, handler.HandleLokiLabels)
		}
	})

	mux.HandleFunc("/loki/api/v1/detected_field/{name}/values", func(w http.ResponseWriter, r *http.Request) {
		span := trace.SpanFromContext(r.Context())
		span.SetAttributes(attribute.String("proxy.route_type", "detected_field_values"))
		fieldName := r.PathValue("name")
		p.fanoutRequest(w, r, func(ctx context.Context, w http.ResponseWriter, results <-chan *proxyresponse.BackendResponse, logger log.Logger) {
			handler.HandleLokiDetectedFieldValues(ctx, w, results, fieldName, logger)
		})
	})

	// Intercept Grafana datasource health check on /loki/api/v1/query.
	// Grafana sends query=vector(1)+vector(1) and expects exactly one vector
	// result with value "2".  Because lokxy fans out to multiple backends the
	// merge would produce duplicates, so we short-circuit with a static
	// response when the proxy itself is healthy.
	mux.HandleFunc("/loki/api/v1/query", func(w http.ResponseWriter, r *http.Request) {
		span := trace.SpanFromContext(r.Context())
		span.SetAttributes(attribute.String("proxy.route_type", "api_route"))

		if handler.IsGrafanaHealthCheck(r) {
			span.SetAttributes(attribute.Bool("proxy.health_check_intercept", true))
			level.Info(logger).Log("msg", "Intercepted Grafana health check, returning static response")
			handler.WriteGrafanaHealthCheckResponse(w, r, logger)
			return
		}

		p.fanoutRequest(w, r, handler.HandleLokiQueries)
	})

	// /loki/api/v1/labels is registered explicitly so it can conditionally
	// inject detected structured-metadata fields as labels.
	//
	// The metadata injection only runs when the incoming request carries a
	// non-empty query parameter because Loki's /detected_fields endpoint
	// requires a valid LogQL stream selector (it rejects empty selectors).
	// When no query is present (e.g. Grafana's initial label-browser load)
	// we fall through to the plain /labels handler.
	mux.HandleFunc("/loki/api/v1/labels", func(w http.ResponseWriter, r *http.Request) {
		span := trace.SpanFromContext(r.Context())
		span.SetAttributes(attribute.String("proxy.route_type", "api_route"))
		incomingQuery := r.URL.Query().Get("query")
		if config.MetadataAsLabels.Enabled && incomingQuery != "" && incomingQuery != "{}" {
			ctx := r.Context()
			fieldsCh := p.backgroundFanoutBytes(ctx, r,
				"/loki/api/v1/detected_fields",
				handler.HandleLokiDetectedFields)
			p.fanoutRequest(w, r, func(c context.Context, w http.ResponseWriter,
				results <-chan *proxyresponse.BackendResponse, logger log.Logger,
			) {
				handler.HandleLokiLabelsWithMetadata(c, w, results, <-fieldsCh,
					config.MetadataAsLabels.Fields, logger)
			})
		} else {
			p.fanoutRequest(w, r, handler.HandleLokiLabels)
		}
	})

	// Variable to hold the API routes and their corresponding handlers
	apiRoutes := map[string]transformFn{
		"/loki/api/v1/query_range":        handler.HandleLokiQueries,
		"/loki/api/v1/series":             handler.HandleLokiSeries,
		"/loki/api/v1/index/stats":        handler.HandleLokiStats,
		"/loki/api/v1/index/volume":       handler.HandleLokiVolume,
		"/loki/api/v1/index/volume_range": handler.HandleLokiVolumeRange,
		"/loki/api/v1/detected_labels":    handler.HandleLokiDetectedLabels,
		"/loki/api/v1/patterns":           handler.HandleLokiPatterns,
		"/loki/api/v1/detected_fields":    handler.HandleLokiDetectedFields,
	}
	for path, handlerFunc := range apiRoutes {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			span := trace.SpanFromContext(r.Context())
			span.SetAttributes(attribute.String("proxy.route_type", "api_route"))
			p.fanoutRequest(w, r, handlerFunc)
		})
	}

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		span := trace.SpanFromContext(r.Context())
		span.SetAttributes(attribute.String("proxy.route_type", "first_response"))
		level.Warn(logger).Log("msg", "No route matched, returning first response only")
		p.fanoutRequest(w, r, forwardFirstResponse)
	})
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, span := traces.CreateSpan(r.Context(), "lokxy_proxy_handler")
		defer span.End()

		path := r.URL.Path
		method := r.Method

		span.SetAttributes(
			attribute.String("path", path),
			attribute.String("method", method),
			attribute.String("query", r.URL.RawQuery),
			attribute.Int("server_groups", len(config.ServerGroups)),
		)

		level.Info(logger).Log("msg", "Handling request", "method", method, "path", path, "query", r.URL.RawQuery)

		// Rewrite metadata field matchers in the query parameter so that
		// structured-metadata fields injected by metadata_as_labels are
		// handled correctly by Loki. Grafana places every selected label
		// inside the stream selector {}, but structured-metadata fields
		// are not stream labels and must be queried differently:
		//   /query, /query_range  -> move to pipeline label-filter stages
		//   everything else       -> strip from the selector entirely
		if config.MetadataAsLabels.Enabled && len(config.MetadataAsLabels.Fields) > 0 {
			if q := r.URL.Query().Get("query"); q != "" {
				var mode rewriteMode
				if path == "/loki/api/v1/query" || path == "/loki/api/v1/query_range" {
					mode = rewriteMoveToPipeline
				} else {
					mode = rewriteStrip
				}
				if rewritten := rewriteMetadataQuery(q, config.MetadataAsLabels.Fields, mode); rewritten != q {
					qParams := r.URL.Query()
					qParams.Set("query", rewritten)
					r.URL.RawQuery = qParams.Encode()
					level.Debug(logger).Log("msg", "Rewrote metadata matchers in query",
						"path", path, "mode", mode, "original", q, "rewritten", rewritten)
				}
			}
		}

		mux.ServeHTTP(w, r.WithContext(ctx))
	}
}

// Forward the first valid response for non-query endpoints
func forwardFirstResponse(_ context.Context, w http.ResponseWriter, results <-chan *proxyresponse.BackendResponse, logger log.Logger) {
	forwarded := false
	for backendResp := range results {
		resp := backendResp.Response
		if !forwarded {
			// Forward the first response
			for key, values := range resp.Header {
				for _, value := range values {
					w.Header().Add(key, value)
				}
			}

			w.Header().Set("Connection", "keep-alive")
			w.WriteHeader(resp.StatusCode)
			if _, err := io.Copy(w, resp.Body); err != nil {
				level.Error(logger).Log("msg", "Failed to copy response body", "err", err)
			}
			forwarded = true
		} else {
			// Drain the body of non-forwarded responses to prevent connection leaks
			if _, err := io.Copy(io.Discard, resp.Body); err != nil {
				level.Error(logger).Log("msg", "Failed to drain response body", "err", err)
			}
		}

		// Close all response bodies to prevent resource leaks
		if err := resp.Body.Close(); err != nil {
			level.Error(logger).Log("msg", "Failed to close response body", "err", err)
		}
	}

	// If no responses were received from any upstream, return an error
	if !forwarded {
		level.Error(logger).Log("msg", "No healthy upstreams available")
		http.Error(w, "No healthy upstreams available", http.StatusBadGateway)
	}
}

// backgroundFanoutBytes fans out to overridePath on all backends, runs fn to
// aggregate the responses into a buffer, and returns a channel that delivers
// the response body bytes when complete. It executes concurrently so the
// caller can start its own fanoutRequest in parallel.
//
// The original request's query string (start=, end=, query=, limit=, etc.) is
// preserved on the cloned request, so field discovery is automatically scoped
// to the same time range and stream selector as the incoming label request.
func (p *proxy) backgroundFanoutBytes(
	ctx context.Context,
	r *http.Request,
	overridePath string,
	fn transformFn,
) <-chan []byte {
	out := make(chan []byte, 1)
	go func() {
		cloned := r.Clone(ctx)
		clonedURL := *r.URL           // copies both Path and RawQuery
		clonedURL.Path = overridePath // override only the path
		cloned.URL = &clonedURL
		rec := httptest.NewRecorder()
		p.fanoutRequest(rec, cloned, fn)
		out <- rec.Body.Bytes()
	}()
	return out
}

// isFieldExposed reports whether the given field name should be surfaced as a
// label. When allowedFields is empty every field is allowed.
func isFieldExposed(name string, allowedFields []string) bool {
	if len(allowedFields) == 0 {
		return true
	}
	for _, f := range allowedFields {
		if f == name {
			return true
		}
	}
	return false
}

func (p *proxy) fanoutRequest(w http.ResponseWriter, r *http.Request, fn transformFn) {
	startTime := time.Now()

	// Read the original request body once
	span := trace.SpanFromContext(r.Context())
	var bodyBytes []byte
	if r.Body != nil {
		var err error
		bodyBytes, err = io.ReadAll(r.Body)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "Failed to read request body")
			level.Error(p.logger).Log("msg", "Failed to read request body", "err", err)
			http.Error(w, "Failed to read request body", http.StatusBadRequest)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	}

	// Function to create a fresh reader for each request
	bodyReader := func() io.ReadCloser {
		return io.NopCloser(bytes.NewReader(bodyBytes))
	}
	results := make(chan *proxyresponse.BackendResponse, len(p.config.ServerGroups))
	ctx := r.Context()

	// Forward requests using the custom RoundTripper
	wg, ctx := errgroup.WithContext(ctx)
	for _, instance := range p.config.ServerGroups {
		wg.Go(func() error {
			upstreamCtx, requestSpan := traces.CreateSpan(ctx, "proxy_upstream_request")
			defer requestSpan.End()

			requestSpan.SetAttributes(
				attribute.String("upstream.name", instance.Name),
				attribute.String("upstream.url", instance.URL),
			)

			client, ok := p.clients[instance.Name]
			if !ok {
				requestSpan.SetStatus(codes.Error, "Missing HTTP client")
				level.Error(p.logger).Log("msg", "Missing HTTP client", "instance", instance.Name)
				return &proxyresponse.BackendError{
					Err:         fmt.Errorf("missing HTTP client for instance %s", instance.Name),
					BackendName: instance.Name,
					BackendURL:  instance.URL,
				}
			}

			targetURL := instance.URL + r.URL.Path
			if r.URL.RawQuery != "" {
				targetURL += "?" + r.URL.RawQuery
			}

			requestSpan.SetAttributes(attribute.String("upstream.target_url", targetURL))

			// Record the request
			metrics.RequestCount.Add(upstreamCtx, 1, metric.WithAttributes(
				attribute.String("path", r.Pattern),
				attribute.String("method", r.Method),
				attribute.String("server_group", instance.Name),
			))

			req, err := http.NewRequestWithContext(upstreamCtx, r.Method, targetURL, bodyReader())
			if err != nil {
				requestSpan.RecordError(err)
				requestSpan.SetStatus(codes.Error, "Failed to create request")
				// Record error count
				metrics.RequestFailures.Add(upstreamCtx, 1, metric.WithAttributes(
					attribute.String("path", r.Pattern),
					attribute.String("method", r.Method),
					attribute.String("server_group", instance.Name),
				))
				level.Error(p.logger).Log("msg", "Failed to create request", "instance", instance.Name, "err", err)
				return &proxyresponse.BackendError{
					Err:         err,
					BackendName: instance.Name,
					BackendURL:  instance.URL,
				}
			}

			req.Header = r.Header.Clone()
			for key, value := range instance.Headers {
				req.Header.Set(key, value)
			}

			traces.InjectTraceToHTTPRequest(upstreamCtx, req)

			if ce := level.Debug(p.logger); ce != nil {
				for name, headers := range redactHeaders(req.Header) {
					for _, h := range headers {
						_ = ce.Log("msg", "Request Header", "Name", name, "Value", h)
					}
				}
			}

			resp, err := client.Do(req)
			if err != nil {
				requestSpan.RecordError(err)
				requestSpan.SetStatus(codes.Error, "Error querying Loki instance")
				// Record error count
				metrics.RequestFailures.Add(upstreamCtx, 1, metric.WithAttributes(
					attribute.String("path", r.Pattern),
					attribute.String("method", r.Method),
					attribute.String("server_group", instance.Name),
				))
				level.Error(p.logger).Log("msg", "Error querying Loki instance", "instance", instance.Name, "err", err)
				return &proxyresponse.BackendError{
					Err:         err,
					BackendName: instance.Name,
					BackendURL:  instance.URL,
				}
			}

			requestSpan.SetAttributes(
				attribute.Int("upstream.status_code", resp.StatusCode),
				attribute.String("upstream.content_type", resp.Header.Get("Content-Type")),
				attribute.Int64("upstream.content_length", resp.ContentLength),
			)

			// Measure response time
			metrics.RequestDuration.Record(upstreamCtx, time.Since(startTime).Seconds(),
				metric.WithAttributes(
					attribute.String("path", r.Pattern),
					attribute.String("method", r.Method),
					attribute.String("server_group", instance.Name),
				),
			)

			// Check for error response (non-2xx status code)
			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				level.Error(p.logger).Log(
					"msg", "Backend returned error response",
					"instance", instance.Name,
					"status", resp.StatusCode,
				)

				// drain the body
				bodyBytes, err := io.ReadAll(resp.Body)
				resp.Body.Close()
				if err != nil {
					level.Error(p.logger).Log(
						"msg", "Failed to read error response body",
						"backend", instance.Name,
						"err", err,
					)
					bodyBytes = []byte("Failed to read error response")
				}
				return &proxyresponse.BackendError{
					Err:         fmt.Errorf("non-2xx response from the upstream: %s", instance.Name),
					BackendName: instance.Name,
					BackendURL:  instance.URL,
					StatusCode:  resp.StatusCode,
					Data:        bodyBytes,
				}
			}
			respBodyBytes, err := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if err != nil {
				requestSpan.RecordError(err)
				requestSpan.SetStatus(codes.Error, "Failed to read upstream response body")
				metrics.RequestFailures.Add(upstreamCtx, 1, metric.WithAttributes(
					attribute.String("path", r.Pattern),
					attribute.String("method", r.Method),
					attribute.String("server_group", instance.Name),
				))
				level.Error(p.logger).Log("msg", "Failed to read upstream response body", "instance", instance.Name, "err", err)
				return &proxyresponse.BackendError{
					Err:         err,
					BackendName: instance.Name,
					BackendURL:  instance.URL,
				}
			}
			resp.Body = io.NopCloser(bytes.NewReader(respBodyBytes))
			resp.ContentLength = int64(len(respBodyBytes))
			results <- &proxyresponse.BackendResponse{
				Response:    resp,
				BackendName: instance.Name,
				BackendURL:  instance.URL,
			}
			return nil
		})
	}
	// Await for all responses
	err := wg.Wait()
	close(results)
	if err != nil {
		berr := &proxyresponse.BackendError{}
		if errors.As(err, &berr) {
			level.Error(p.logger).Log("msg", "Failed to fetch responses", "err", err, "instance", berr.BackendName)
			if berr.StatusCode != 0 {
				proxyresponse.ForwardBackendError(w, berr.BackendName, berr.StatusCode, berr.Data, p.logger)
			} else {
				proxyresponse.ForwardConnectionError(w, berr, p.logger)
			}
		} else {
			level.Error(p.logger).Log("msg", "Failed to fetch responses from upstreams", "err", err)
			http.Error(w, "No healthy upstreams available", http.StatusBadGateway)
		}

		for remaining := range results {
			if remaining.Response != nil && remaining.Response.Body != nil {
				_, err := io.Copy(io.Discard, remaining.Response.Body)
				if err != nil {
					level.Error(p.logger).Log("msg", "Failed to read response body", "err", err, "instance", remaining.BackendName)
				}
				if err := remaining.Response.Body.Close(); err != nil {
					level.Error(p.logger).Log("msg", "Failed to close response body", "err", err, "instance", remaining.BackendName)
				}
			}
		}
		return
	}

	// Combine responses into expected response
	fn(r.Context(), w, results, p.logger)
}
