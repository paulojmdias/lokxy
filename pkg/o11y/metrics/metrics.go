package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	// Define Prometheus metrics
	RequestCount    *prometheus.CounterVec
	RequestDuration *prometheus.HistogramVec
	RequestFailures *prometheus.CounterVec
)

// Initialize metrics and register them with the default registry
func InitMetrics() {
	RequestCount = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "lokxy_request_count_total",
		Help: "Total number of requests processed by the proxy",
	}, []string{"path", "method", "server_group"})

	RequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "lokxy_request_duration_seconds",
		Help:    "Duration of proxy requests in seconds",
		Buckets: prometheus.DefBuckets,
	}, []string{"path", "method", "server_group"})

	RequestFailures = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "lokxy_request_failures_total",
		Help: "Total number of failed requests.",
	}, []string{"path", "method", "server_group"})
}

// PrometheusHandler returns an HTTP handler for Prometheus metrics
func PrometheusHandler() http.Handler {
	return promhttp.Handler()
}
