package proxy

import (
	"compress/gzip"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	cfg "github.com/paulojmdias/lokxy/pkg/config"
	"github.com/paulojmdias/lokxy/pkg/o11y/metrics"
	"github.com/paulojmdias/lokxy/pkg/proxy/handler"
)

// Varible to hold the API routes and their corresponding handlers
var apiRoutes = map[string]func(http.ResponseWriter, <-chan *http.Response, log.Logger){
	"/loki/api/v1/query":       handler.HandleLokiQueries,
	"/loki/api/v1/query_range": handler.HandleLokiQueries,
	"/loki/api/v1/series":      handler.HandleLokiSeries,
	"/loki/api/v1/index/stats": handler.HandleLokiStats,
	"/loki/api/v1/labels":      handler.HandleLokiLabels,
}

// CustomRoundTripper intercepts the request and response
type CustomRoundTripper struct {
	rt     http.RoundTripper
	logger log.Logger
}

// RoundTrip method allows us to inspect and modify requests/responses
func (c *CustomRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	level.Debug(c.logger).Log("msg", "Custom RoundTrip", "url", req.URL, "headers", req.Header)

	// Perform the actual request
	resp, err := c.rt.RoundTrip(req)
	if err != nil {
		return nil, err
	}

	// Log and handle the response as needed
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gzReader, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, err
		}
		defer gzReader.Close()
		resp.Body = gzReader
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

	// Create HTTP transport with the custom TLS configuration
	transport := &http.Transport{
		TLSClientConfig: tlsConfig,
	}

	client := &http.Client{
		Timeout:   time.Duration(instance.Timeout) * time.Second,
		Transport: &CustomRoundTripper{rt: transport, logger: logger},
	}

	return client, nil
}

func ProxyHandler(w http.ResponseWriter, r *http.Request, config *cfg.Config, logger log.Logger) {
	startTime := time.Now()
	path := r.URL.Path
	method := r.Method

	level.Info(logger).Log("msg", "Handling request", "method", method, "path", path, "query", r.URL.RawQuery)

	var wg sync.WaitGroup
	results := make(chan *http.Response, len(config.ServerGroups))
	errors := make(chan error, len(config.ServerGroups))

	// Forward requests using the custom RoundTripper
	for _, instance := range config.ServerGroups {
		wg.Add(1)
		go func(instance cfg.ServerGroup) {
			defer wg.Done()

			// Create an HTTP client for each instance
			client, err := createHTTPClient(instance, logger)
			if err != nil {
				metrics.RequestFailures.WithLabelValues(path, method, instance.Name).Inc() // Record error count
				level.Error(logger).Log("msg", "Failed to create HTTP client", "instance", instance.Name, "err", err)
				return
			}

			targetURL := instance.URL + r.URL.Path
			if r.URL.RawQuery != "" {
				targetURL += "?" + r.URL.RawQuery
			}

			// Record the request
			metrics.RequestCount.WithLabelValues(path, method, instance.Name).Inc()

			req, err := http.NewRequest(r.Method, targetURL, r.Body)
			if err != nil {
				metrics.RequestFailures.WithLabelValues(path, method, instance.Name).Inc() // Record error count
				level.Error(logger).Log("msg", "Failed to create request", "instance", instance.Name, "err", err)
				errors <- err
				return
			}

			req.Header = r.Header.Clone()
			for key, value := range instance.Headers {
				req.Header.Set(key, value)
			}

			for name, headers := range req.Header {
				for _, h := range headers {
					level.Debug(logger).Log("msg", "Request Header", "Name", name, "Value", h)
				}
			}

			resp, err := client.Do(req)
			if err != nil {
				metrics.RequestFailures.WithLabelValues(path, method, instance.Name).Inc() // Record error count
				level.Error(logger).Log("msg", "Error querying Loki instance", "instance", instance.Name, "err", err)
				errors <- err
				return
			}

			// Measure response time
			duration := time.Since(startTime).Seconds()
			metrics.RequestDuration.WithLabelValues(path, method, instance.Name).Observe(duration)

			results <- resp
		}(instance)
	}

	wg.Wait()
	close(results)
	close(errors)

	if handlerFunc, ok := apiRoutes[path]; ok {
		handlerFunc(w, results, logger) // Call appropriate handler
	} else if strings.HasPrefix(path, "/loki/api/v1/label/") && strings.HasSuffix(path, "/values") {
		handler.HandleLokiLabels(w, results, logger)
	} else if strings.HasPrefix(path, "/loki/api/v1/tail") {
		handler.HandleTailWebSocket(w, r, config, logger)
	} else {
		level.Warn(logger).Log("msg", "No route matched, returning first response only")
		forwardFirstResponse(w, results)
	}
}

// Forward the first valid response for non-query endpoints
func forwardFirstResponse(w http.ResponseWriter, results <-chan *http.Response) {
	for resp := range results {
		// Directly copy all headers and body from Loki response to Grafana
		for key, values := range resp.Header {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}

		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(resp.StatusCode)
		_, err := io.Copy(w, resp.Body) // Forward the body as-is
		if err != nil {
			if err := level.Error(logger).Log("msg", "Failed to copy response body", "err", err); err != nil {
				fmt.Println("Logging error:", err)
			}
			return
		}
		resp.Body.Close()
	}
}
