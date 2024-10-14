package metrics

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// initTestRegistry initializes a custom Prometheus registry with the metrics defined in metrics.go.
func initTestRegistry() *prometheus.Registry {
	registry := prometheus.NewRegistry()

	RequestCount = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "lokxy_request_count_total",
		Help: "Total number of requests processed by the proxy",
	}, []string{"path", "method", "server_group"})

	RequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "lokxy_request_duration_seconds",
		Help:    "Duration of proxy requests in seconds",
		Buckets: prometheus.DefBuckets,
	}, []string{"path", "method", "server_group"})

	RequestFailures = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "lokxy_request_failures_total",
		Help: "Total number of failed requests.",
	}, []string{"path", "method", "server_group"})

	// Register metrics with the custom registry
	registry.MustRegister(RequestCount)
	registry.MustRegister(RequestDuration)
	registry.MustRegister(RequestFailures)

	// Increment the counters and observe the histogram to ensure they have values
	RequestCount.WithLabelValues("/", "GET", "test_group").Inc()
	RequestDuration.WithLabelValues("/", "GET", "test_group").Observe(1.0)
	RequestFailures.WithLabelValues("/", "GET", "test_group").Inc()

	return registry
}

func TestInitMetrics(t *testing.T) {
	// Initialize the custom registry with test-specific metrics
	registry := initTestRegistry()

	// Check if the metrics are registered
	metricFamilies, err := registry.Gather()
	if err != nil {
		t.Fatalf("Error gathering metrics: %v", err)
	}

	if len(metricFamilies) == 0 {
		t.Errorf("Expected metrics to be registered, but none were found")
	}

	// Check if specific metrics are registered
	expectedMetrics := []string{
		"lokxy_request_count_total",
		"lokxy_request_duration_seconds",
		"lokxy_request_failures_total",
	}

	for _, metric := range expectedMetrics {
		found := false
		for _, mf := range metricFamilies {
			if *mf.Name == metric {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected metric %s to be registered, but it was not found", metric)
		}
	}
}

func TestPrometheusHandler(t *testing.T) {
	// Initialize the custom registry
	registry := initTestRegistry()

	// Create a new HTTP request
	req, err := http.NewRequest("GET", "/metrics", nil)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}

	// Create a new HTTP recorder
	rr := httptest.NewRecorder()

	// Create a Prometheus handler using the custom registry
	handler := promhttp.HandlerFor(registry, promhttp.HandlerOpts{})

	// Serve the HTTP request
	handler.ServeHTTP(rr, req)

	// Check the status code
	if status := rr.Code; status != http.StatusOK {
		t.Errorf("Handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	// Check the response body to ensure metrics are present
	if len(rr.Body.Bytes()) == 0 {
		t.Errorf("Expected response body to contain metrics, but it was empty")
	}
}
