package proxy

import (
	"bytes"
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
	traces "github.com/paulojmdias/lokxy/pkg/o11y/tracing"
	proxyErrors "github.com/paulojmdias/lokxy/pkg/proxy/errors"
	"github.com/paulojmdias/lokxy/pkg/proxy/handler"
	"go.opentelemetry.io/otel/codes"
)

var apiRoutes = map[string]func(http.ResponseWriter, <-chan *http.Response, log.Logger){
	"/loki/api/v1/query":       handler.HandleLokiQueries,
	"/loki/api/v1/query_range": handler.HandleLokiQueries,
	"/loki/api/v1/series":      handler.HandleLokiSeries,
	"/loki/api/v1/index/stats": handler.HandleLokiStats,
	"/loki/api/v1/labels":      handler.HandleLokiLabels,
}

type CustomRoundTripper struct {
	rt     http.RoundTripper
	logger log.Logger
}

func (c *CustomRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := c.rt.RoundTrip(req)
	if err != nil {
		return nil, err
	}

	if resp.Header.Get("Content-Encoding") == "gzip" {
		gzReader, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, err
		}
		resp.Body = io.NopCloser(gzReader)
	}

	return resp, nil
}

func createHTTPClient(instance cfg.ServerGroup, logger log.Logger) (*http.Client, error) {
	tlsConfig := &tls.Config{
		InsecureSkipVerify: instance.HTTPClientConfig.TLSConfig.InsecureSkipVerify,
	}

	if instance.HTTPClientConfig.TLSConfig.CAFile != "" {
		caCert, err := os.ReadFile(instance.HTTPClientConfig.TLSConfig.CAFile)
		if err != nil {
			return nil, err
		}
		caCertPool := x509.NewCertPool()
		caCertPool.AppendCertsFromPEM(caCert)
		tlsConfig.RootCAs = caCertPool
	}

	if instance.HTTPClientConfig.TLSConfig.CertFile != "" && instance.HTTPClientConfig.TLSConfig.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(instance.HTTPClientConfig.TLSConfig.CertFile, instance.HTTPClientConfig.TLSConfig.KeyFile)
		if err != nil {
			return nil, err
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}

	transport := &http.Transport{TLSClientConfig: tlsConfig}
	client := &http.Client{
		Timeout:   time.Duration(instance.Timeout) * time.Second,
		Transport: &CustomRoundTripper{rt: transport, logger: logger},
	}

	return client, nil
}

func ProxyHandler(w http.ResponseWriter, r *http.Request, config *cfg.Config, logger log.Logger) {
	ctx := r.Context()
	ctx, span := traces.CreateSpan(ctx, "lokxy_proxy_handler")
	defer span.End()

	results := make(chan *http.Response, len(config.ServerGroups))
	errors := make(chan error, len(config.ServerGroups))

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, proxyErrors.ErrReadBodyFailed.Error())
		level.Error(logger).Log("msg", proxyErrors.ErrReadBodyFailed.Error(), "err", err)
		proxyErrors.WriteJSONError(w, http.StatusInternalServerError, proxyErrors.ErrReadBodyFailed.Error())
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	bodyReader := func() io.ReadCloser { return io.NopCloser(bytes.NewReader(bodyBytes)) }

	var wg sync.WaitGroup
	for _, instance := range config.ServerGroups {
		wg.Add(1)
		go func(instance cfg.ServerGroup) {
			defer wg.Done()

			client, err := createHTTPClient(instance, logger)
			if err != nil {
				errors <- err
				return
			}

			targetURL := instance.URL + r.URL.Path
			if r.URL.RawQuery != "" {
				targetURL += "?" + r.URL.RawQuery
			}

			req, err := http.NewRequestWithContext(ctx, r.Method, targetURL, bodyReader())
			if err != nil {
				errors <- err
				return
			}
			req.Header = r.Header.Clone()

			resp, err := client.Do(req)
			if err != nil {
				errors <- err
				return
			}

			results <- resp
		}(instance)
	}

	go func() {
		wg.Wait()
		close(results)
		close(errors)
	}()

	if handlerFunc, ok := apiRoutes[r.URL.Path]; ok {
		handlerFunc(w, results, logger)
		return
	}

	if strings.HasPrefix(r.URL.Path, "/loki/api/v1/label/") && strings.HasSuffix(r.URL.Path, "/values") {
		handler.HandleLokiLabels(w, results, logger)
		return
	}

	if strings.HasPrefix(r.URL.Path, "/loki/api/v1/tail") {
		handler.HandleTailWebSocket(w, r, config, logger)
		return
	}

	// fallback
	select {
	case resp := <-results:
		for key, values := range resp.Header {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
		resp.Body.Close()
	default:
		proxyErrors.WriteJSONError(w, http.StatusBadGateway, proxyErrors.ErrNoUpstream.Error())
	}
}
