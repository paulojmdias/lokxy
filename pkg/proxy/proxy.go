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
	"os"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/errgroup"

	cfg "github.com/paulojmdias/lokxy/pkg/config"
	"github.com/paulojmdias/lokxy/pkg/o11y/metrics"
	traces "github.com/paulojmdias/lokxy/pkg/o11y/tracing"
	"github.com/paulojmdias/lokxy/pkg/proxy/handler"
	"github.com/paulojmdias/lokxy/pkg/proxy/proxyresponse"
	"github.com/paulojmdias/lokxy/pkg/routing"
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
	// Proxy fans requests out to the configured server groups. The loaded
	// configuration and the HTTP clients built from it are held in an
	// atomically swappable snapshot so the configuration can be reloaded at
	// runtime without a restart.
	Proxy struct {
		logger log.Logger
		state  atomic.Pointer[proxyState]
	}

	// proxyState is an immutable snapshot of a loaded configuration and the
	// HTTP clients built from it. Each request loads one snapshot and uses it
	// for its whole lifetime, so a request never mixes old and new config.
	proxyState struct {
		config  *cfg.Config
		clients map[string]*http.Client
	}

	transformFn func(context.Context, http.ResponseWriter, <-chan *proxyresponse.BackendResponse, []string, log.Logger)

	// softFailure is an upstream failure from a server group configured with
	// ignore_error or downgrade_error. It does not fail the overall query.
	softFailure struct {
		berr      *proxyresponse.BackendError
		downgrade bool // true => surface as a warning; false => ignore silently
	}
)

// New builds a Proxy from the given configuration. It fails if any server
// group's HTTP client cannot be created (e.g. an unreadable TLS file).
func New(logger log.Logger, config *cfg.Config) (*Proxy, error) {
	p := &Proxy{logger: logger}
	if err := p.ApplyConfig(config); err != nil {
		return nil, err
	}
	return p, nil
}

// ApplyConfig atomically replaces the proxy's configuration and HTTP clients.
// If any client cannot be built, the previous configuration stays active and
// the error is returned. In-flight requests keep using the snapshot they
// started with; idle connections of the replaced clients are closed.
func (p *Proxy) ApplyConfig(config *cfg.Config) error {
	state, err := buildState(config, p.logger)
	if err != nil {
		return err
	}
	old := p.state.Swap(state)
	if old != nil {
		for _, client := range old.clients {
			client.CloseIdleConnections()
		}
	}
	return nil
}

func buildState(config *cfg.Config, logger log.Logger) (*proxyState, error) {
	clients := make(map[string]*http.Client, len(config.ServerGroups))
	for _, instance := range config.ServerGroups {
		client, err := createHTTPClient(instance, logger)
		if err != nil {
			return nil, fmt.Errorf("failed to create HTTP client for server group %q: %w", instance.Name, err)
		}
		clients[instance.Name] = client
	}
	return &proxyState{config: config, clients: clients}, nil
}

// Handler returns the proxy's request handler. The routes are fixed; the
// configuration backing them is read from the current snapshot per request.
func (p *Proxy) Handler() func(http.ResponseWriter, *http.Request) {
	logger := p.logger

	mux := http.NewServeMux()
	mux.HandleFunc("/loki/api/v1/tail", func(w http.ResponseWriter, r *http.Request) {
		span := trace.SpanFromContext(r.Context())
		span.SetAttributes(attribute.String("proxy.route_type", "websocket"))
		handler.HandleTailWebSocket(r.Context(), w, r, p.state.Load().config, logger)
	})

	mux.HandleFunc("/loki/api/v1/label/{name}/values", func(w http.ResponseWriter, r *http.Request) {
		span := trace.SpanFromContext(r.Context())
		span.SetAttributes(attribute.String("proxy.route_type", "label_values"))
		p.fanoutRequest(w, r, handler.HandleLokiLabels)
	})

	mux.HandleFunc("/loki/api/v1/detected_field/{name}/values", func(w http.ResponseWriter, r *http.Request) {
		span := trace.SpanFromContext(r.Context())
		span.SetAttributes(attribute.String("proxy.route_type", "detected_field_values"))
		fieldName := r.PathValue("name")
		p.fanoutRequest(w, r, func(ctx context.Context, w http.ResponseWriter, results <-chan *proxyresponse.BackendResponse, _ []string, logger log.Logger) {
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

	// Variable to hold the API routes and their corresponding handlers
	apiRoutes := map[string]transformFn{
		"/loki/api/v1/query_range":        handler.HandleLokiQueries,
		"/loki/api/v1/series":             handler.HandleLokiSeries,
		"/loki/api/v1/index/stats":        handler.HandleLokiStats,
		"/loki/api/v1/labels":             handler.HandleLokiLabels,
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
			semconv.URLPath(path),
			semconv.HTTPRequestMethodKey.String(method),
			semconv.URLQuery(r.URL.RawQuery),
			attribute.Int("lokxy.server_groups", len(p.state.Load().config.ServerGroups)),
		)

		level.Info(logger).Log("msg", "Handling request", "method", method, "path", path, "query", r.URL.RawQuery)

		mux.ServeHTTP(w, r.WithContext(ctx))
	}
}

// Forward the first valid response for non-query endpoints
func forwardFirstResponse(_ context.Context, w http.ResponseWriter, results <-chan *proxyresponse.BackendResponse, _ []string, logger log.Logger) {
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

// instanceKV returns the structured-logging keyvals identifying a server group:
// its name plus every configured label flattened as "sg_<key>" fields, so log
// lines can be filtered by group label. Keys are emitted in sorted order for
// stable output.
func instanceKV(sg cfg.ServerGroup) []interface{} {
	kv := make([]interface{}, 0, 2+2*len(sg.Labels))
	kv = append(kv, "instance", sg.Name)
	if len(sg.Labels) > 0 {
		keys := make([]string, 0, len(sg.Labels))
		for k := range sg.Labels {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			kv = append(kv, "sg_"+k, sg.Labels[k])
		}
	}
	return kv
}

// emptyResults returns an already-closed, empty results channel. It lets a
// transform function produce an empty successful response (optionally carrying
// warnings) without any upstream request, used when label routing matches no
// server group or strips a query down to an empty selector.
func emptyResults() <-chan *proxyresponse.BackendResponse {
	ch := make(chan *proxyresponse.BackendResponse)
	close(ch)
	return ch
}

func (p *Proxy) fanoutRequest(w http.ResponseWriter, r *http.Request, fn transformFn) {
	startTime := time.Now()

	// Load one snapshot for the whole request so the server groups and the
	// clients built from them always match, even across a concurrent reload.
	st := p.state.Load()

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

	// Label-based routing. When any server group defines labels, inspect the
	// query's label matchers: if they reference a routing-label key, restrict
	// the fan-out to the matching groups and strip those virtual labels from the
	// forwarded query. Any failure to extract/parse a selector leaves all groups
	// selected, so routing never breaks existing behavior.
	groups := st.config.ServerGroups
	forwardRawQuery := r.URL.RawQuery
	forwardBody := bodyBytes
	if routingKeys := routing.RoutingKeys(groups); len(routingKeys) > 0 {
		if matchers, ok := routing.ExtractMatchers(r, bodyBytes); ok {
			groups = routing.SelectGroups(groups, matchers, routingKeys)
			span.SetAttributes(attribute.Int("lokxy.routed_groups", len(groups)))
			level.Debug(p.logger).Log("msg", "Label routing applied",
				"matched", len(groups), "total", len(st.config.ServerGroups))

			if len(groups) == 0 {
				level.Warn(p.logger).Log("msg", "No server group matched routing labels", "query", r.URL.Query().Get("query"))
				fn(r.Context(), w, emptyResults(), []string{"no server group matched the routing labels in the query"}, p.logger)
				return
			}

			var strippedEmpty bool
			forwardRawQuery, forwardBody, strippedEmpty = routing.StripRequest(r, bodyBytes, routingKeys)
			if strippedEmpty {
				level.Warn(p.logger).Log("msg", "Query selects only by routing label", "query", r.URL.Query().Get("query"))
				fn(r.Context(), w, emptyResults(), []string{"query selects only by routing label; add at least one stream matcher to return results"}, p.logger)
				return
			}
		}
	}

	// Function to create a fresh reader for each request
	bodyReader := func() io.ReadCloser {
		return io.NopCloser(bytes.NewReader(forwardBody))
	}
	results := make(chan *proxyresponse.BackendResponse, len(groups))
	// softErrs collects failures from server groups configured with
	// ignore_error/downgrade_error so they do not fail the overall query.
	softErrs := make(chan softFailure, len(groups))
	ctx := r.Context()

	// Forward requests using the custom RoundTripper
	wg, ctx := errgroup.WithContext(ctx)
	for _, instance := range groups {
		wg.Go(func() error {
			// Classify this server group's error-handling policy. A required
			// group (the default) fails the whole query on error; an optional
			// group's failure is recorded as a soft failure instead. The two
			// flags are mutually exclusive (enforced by config.Validate).
			optional := instance.IgnoreError || instance.DowngradeError
			downgrade := instance.DowngradeError
			recordFailure := func(berr *proxyresponse.BackendError) error {
				if !optional {
					return berr
				}
				softErrs <- softFailure{berr: berr, downgrade: downgrade}
				return nil
			}

			upstreamCtx, requestSpan := traces.CreateSpan(ctx, "proxy_upstream_request", trace.WithSpanKind(trace.SpanKindClient))
			defer requestSpan.End()

			requestSpan.SetAttributes(
				attribute.String("upstream.name", instance.Name),
				attribute.String("upstream.url", instance.URL),
			)

			client, ok := st.clients[instance.Name]
			if !ok {
				requestSpan.SetStatus(codes.Error, "Missing HTTP client")
				level.Error(p.logger).Log(append(instanceKV(instance), "msg", "Missing HTTP client")...)
				return recordFailure(&proxyresponse.BackendError{
					Err:         fmt.Errorf("missing HTTP client for instance %s", instance.Name),
					BackendName: instance.Name,
					BackendURL:  instance.URL,
				})
			}

			targetURL := instance.URL + r.URL.Path
			if forwardRawQuery != "" {
				targetURL += "?" + forwardRawQuery
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
				level.Error(p.logger).Log(append(instanceKV(instance), "msg", "Failed to create request", "err", err)...)
				return recordFailure(&proxyresponse.BackendError{
					Err:         err,
					BackendName: instance.Name,
					BackendURL:  instance.URL,
				})
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
				level.Error(p.logger).Log(append(instanceKV(instance), "msg", "Error querying Loki instance", "err", err)...)
				return recordFailure(&proxyresponse.BackendError{
					Err:         err,
					BackendName: instance.Name,
					BackendURL:  instance.URL,
				})
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
				level.Error(p.logger).Log(append(instanceKV(instance),
					"msg", "Backend returned error response",
					"status", resp.StatusCode,
				)...)

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
				return recordFailure(&proxyresponse.BackendError{
					Err:         fmt.Errorf("non-2xx response from the upstream: %s", instance.Name),
					BackendName: instance.Name,
					BackendURL:  instance.URL,
					StatusCode:  resp.StatusCode,
					Data:        bodyBytes,
				})
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
				level.Error(p.logger).Log(append(instanceKV(instance), "msg", "Failed to read upstream response body", "err", err)...)
				return recordFailure(&proxyresponse.BackendError{
					Err:         err,
					BackendName: instance.Name,
					BackendURL:  instance.URL,
				})
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
	close(softErrs)
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

	// No required server group failed. Process soft failures (optional groups
	// configured with ignore_error/downgrade_error): downgraded ones become
	// warnings on the merged response, ignored ones are silent.
	var warnings []string
	var lastSoft *proxyresponse.BackendError
	softCount := 0
	for sf := range softErrs {
		softCount++
		lastSoft = sf.berr
		outcome := "ignored"
		if sf.downgrade {
			outcome = "downgraded"
			msg := sf.berr.Error()
			if sf.berr.StatusCode != 0 {
				msg = fmt.Sprintf("status %d: %s", sf.berr.StatusCode, strings.TrimSpace(string(sf.berr.Data)))
			}
			warnings = append(warnings, fmt.Sprintf("server group %q error downgraded to warning: %s", sf.berr.BackendName, msg))
			level.Warn(p.logger).Log("msg", "Server group error downgraded to warning", "instance", sf.berr.BackendName, "err", sf.berr.Error())
		} else {
			level.Debug(p.logger).Log("msg", "Server group error ignored", "instance", sf.berr.BackendName, "err", sf.berr.Error())
		}
		metrics.RequestDegraded.Add(r.Context(), 1, metric.WithAttributes(
			attribute.String("path", r.Pattern),
			attribute.String("method", r.Method),
			attribute.String("server_group", sf.berr.BackendName),
			attribute.String("outcome", outcome),
		))
	}

	// If every contributing server group was optional and all of them failed,
	// there is no data to merge. Forward the last failure rather than return a
	// misleading empty success.
	if len(results) == 0 && softCount > 0 {
		level.Error(p.logger).Log("msg", "All optional server groups failed", "instance", lastSoft.BackendName)
		if lastSoft.StatusCode != 0 {
			proxyresponse.ForwardBackendError(w, lastSoft.BackendName, lastSoft.StatusCode, lastSoft.Data, p.logger)
		} else {
			proxyresponse.ForwardConnectionError(w, lastSoft, p.logger)
		}
		return
	}

	// Combine responses into expected response
	fn(r.Context(), w, results, warnings, p.logger)
}
