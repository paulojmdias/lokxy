package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"sort"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/paulojmdias/lokxy/pkg/o11y/metrics"
	traces "github.com/paulojmdias/lokxy/pkg/o11y/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
)

// LokiPatternEntry represents a single pattern block from Loki.
type LokiPatternEntry struct {
	Pattern string    `json:"pattern"`
	Samples [][]int64 `json:"samples"` // [[timestamp, count], ...]
}

// LokiPatternsResponse mirrors Loki's response for /loki/api/v1/patterns.
type LokiPatternsResponse struct {
	Status string             `json:"status,omitempty"`
	Data   []LokiPatternEntry `json:"data"`
}

// HandleLokiPatterns aggregates /patterns responses from multiple Loki instances.
func HandleLokiPatterns(ctx context.Context, w http.ResponseWriter, results <-chan *http.Response, logger log.Logger) {
	ctx, span := traces.CreateSpan(ctx, "handle_patterns")
	defer span.End()

	// merged[pattern][timestamp] = count
	merged := make(map[string]map[int64]int64)

	for resp := range results {
		if resp == nil || resp.Body == nil {
			_, errSpan := traces.CreateSpan(ctx, "patterns.nil_response")
			errSpan.RecordError(io.ErrUnexpectedEOF)
			errSpan.SetStatus(codes.Error, "nil upstream response/body")
			errSpan.End()

			if metrics.RequestFailures != nil {
				metrics.RequestFailures.Add(ctx, 1, metric.WithAttributes(
					attribute.String("path", "/loki/api/v1/patterns"),
					attribute.String("method", "GET"),
					attribute.String("error_type", "nil_response"),
				))
			}

			level.Warn(logger).Log("msg", "nil response or body received for patterns")
			continue
		}

		func() {
			defer resp.Body.Close()

			body, err := io.ReadAll(resp.Body)
			if err != nil {
				_, errSpan := traces.CreateSpan(ctx, "patterns.read_body")
				errSpan.RecordError(err)
				errSpan.SetStatus(codes.Error, "failed to read response body")
				errSpan.End()

				if metrics.RequestFailures != nil {
					metrics.RequestFailures.Add(ctx, 1, metric.WithAttributes(
						attribute.String("path", "/loki/api/v1/patterns"),
						attribute.String("method", "GET"),
						attribute.String("error_type", "read_body_failed"),
					))
				}

				level.Error(logger).Log("msg", "failed to read patterns response body", "err", err)
				return
			}

			level.Debug(logger).Log("msg", "received body for patterns", "body", string(body))

			var lr LokiPatternsResponse
			if err := json.Unmarshal(body, &lr); err != nil {
				_, errSpan := traces.CreateSpan(ctx, "patterns.unmarshal")
				errSpan.RecordError(err)
				errSpan.SetStatus(codes.Error, "failed to unmarshal patterns response")
				errSpan.End()

				if metrics.RequestFailures != nil {
					metrics.RequestFailures.Add(ctx, 1, metric.WithAttributes(
						attribute.String("path", "/loki/api/v1/patterns"),
						attribute.String("method", "GET"),
						attribute.String("error_type", "json_unmarshal_failed"),
					))
				}

				level.Error(logger).Log("msg", "failed to unmarshal patterns response", "err", err)
				return
			}

			for _, entry := range lr.Data {
				if _, ok := merged[entry.Pattern]; !ok {
					merged[entry.Pattern] = make(map[int64]int64)
				}
				for _, pair := range entry.Samples {
					// Defensive parsing: accept [ts,count] of len>=2, ignore bad shapes.
					if len(pair) < 2 {
						continue
					}
					ts := pair[0]
					cnt := pair[1]
					merged[entry.Pattern][ts] += cnt
				}
			}
		}()
	}

	// Rebuild final response: sort timestamps within each pattern; sort patterns.
	out := make([]LokiPatternEntry, 0, len(merged))
	for pattern, tsMap := range merged {
		// Collect and sort timestamps.
		timestamps := make([]int64, 0, len(tsMap))
		for ts := range tsMap {
			timestamps = append(timestamps, ts)
		}
		slices.Sort(timestamps)

		samples := make([][]int64, 0, len(timestamps))
		for _, ts := range timestamps {
			samples = append(samples, []int64{ts, tsMap[ts]})
		}

		out = append(out, LokiPatternEntry{
			Pattern: pattern,
			Samples: samples,
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Pattern < out[j].Pattern })

	final := LokiPatternsResponse{
		Status: "success",
		Data:   out,
	}

	w.Header().Set("Content-Type", "application/json")

	// Encode span so serialization errors are visible
	_, encSpan := traces.CreateSpan(ctx, "patterns.encode_response")
	if err := json.NewEncoder(w).Encode(final); err != nil {
		encSpan.RecordError(err)
		encSpan.SetStatus(codes.Error, "failed to encode final patterns response")
		level.Error(logger).Log("msg", "failed to encode final patterns response", "err", err)
	}
	encSpan.End()
}
