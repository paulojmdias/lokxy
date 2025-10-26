package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sort"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/paulojmdias/lokxy/pkg/o11y/metrics"
	traces "github.com/paulojmdias/lokxy/pkg/o11y/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
)

// DetectedLabel represents a single detected label entry from Loki
type DetectedLabel struct {
	Label       string `json:"label"`
	Cardinality int    `json:"cardinality"`
}

// LokiDetectedLabelsResponse represents the structure of the detected labels response from Loki
type LokiDetectedLabelsResponse struct {
	DetectedLabels []DetectedLabel `json:"detectedLabels"`
}

// HandleLokiDetectedLabels aggregates detected labels from multiple Loki instances
func HandleLokiDetectedLabels(ctx context.Context, w http.ResponseWriter, results <-chan *http.Response, logger log.Logger) {
	ctx, span := traces.CreateSpan(ctx, "handle_detected_labels")
	defer span.End()

	mergedLabels := make(map[string]int)

	for resp := range results {
		if resp == nil || resp.Body == nil {
			_, responseSpan := traces.CreateSpan(ctx, "detected_labels.nil_response")
			responseSpan.RecordError(io.ErrUnexpectedEOF)
			responseSpan.SetStatus(codes.Error, "nil upstream response/body")
			if metrics.RequestFailures != nil {
				metrics.RequestFailures.Add(ctx, 1, metric.WithAttributes(
					attribute.String("path", "/loki/api/v1/detected_labels"),
					attribute.String("method", "GET"),
					attribute.String("error_type", "nil_response"),
				))
			}
			responseSpan.End()

			level.Error(logger).Log("msg", "Nil upstream response/body for detected labels")
			continue
		}

		bodyBytes, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			_, responseSpan := traces.CreateSpan(ctx, "detected_labels.read_body")
			responseSpan.RecordError(err)
			responseSpan.SetStatus(codes.Error, "Failed to read response body")
			if metrics.RequestFailures != nil {
				metrics.RequestFailures.Add(ctx, 1, metric.WithAttributes(
					attribute.String("path", "/loki/api/v1/detected_labels"),
					attribute.String("method", "GET"),
					attribute.String("error_type", "read_body_failed"),
				))
			}
			responseSpan.End()

			level.Error(logger).Log("msg", "Failed to read response body", "err", err)
			continue
		}

		level.Debug(logger).Log("msg", "Received body for detected labels", "body", string(bodyBytes))

		var lokiResponse LokiDetectedLabelsResponse
		if err := json.Unmarshal(bodyBytes, &lokiResponse); err != nil {
			_, responseSpan := traces.CreateSpan(ctx, "detected_labels.unmarshal")
			responseSpan.RecordError(err)
			responseSpan.SetStatus(codes.Error, "Failed to unmarshal detected labels response")
			if metrics.RequestFailures != nil {
				metrics.RequestFailures.Add(ctx, 1, metric.WithAttributes(
					attribute.String("path", "/loki/api/v1/detected_labels"),
					attribute.String("method", "GET"),
					attribute.String("error_type", "json_unmarshal_failed"),
				))
			}
			responseSpan.End()

			level.Error(logger).Log("msg", "Failed to unmarshal detected labels response", "err", err)
			continue
		}

		// Merge the detected labels - sum cardinalities
		for _, labelData := range lokiResponse.DetectedLabels {
			mergedLabels[labelData.Label] += labelData.Cardinality
		}
	}

	// Convert merged data to response format
	finalDetectedLabels := make([]DetectedLabel, 0, len(mergedLabels))
	for label, cardinality := range mergedLabels {
		finalDetectedLabels = append(finalDetectedLabels, DetectedLabel{
			Label:       label,
			Cardinality: cardinality,
		})
	}

	// Sort labels for consistency
	sort.Slice(finalDetectedLabels, func(i, j int) bool {
		return finalDetectedLabels[i].Label < finalDetectedLabels[j].Label
	})

	// Prepare the final response in Loki format
	finalResponse := LokiDetectedLabelsResponse{
		DetectedLabels: finalDetectedLabels,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(finalResponse); err != nil {
		_, encSpan := traces.CreateSpan(ctx, "detected_labels.encode_response")
		encSpan.RecordError(err)
		encSpan.SetStatus(codes.Error, "Failed to encode final detected labels response")
		encSpan.End()

		level.Error(logger).Log("msg", "Failed to encode final detected labels response", "err", err)
	}
}
