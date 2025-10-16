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

// ===================== Common output types =====================

// DetectedFieldOut is the unified output for /detected_fields
type DetectedFieldOut struct {
	Label       string   `json:"label"`
	Type        string   `json:"type,omitempty"`
	Cardinality int      `json:"cardinality"`
	Parsers     []string `json:"parsers,omitempty"`
}

// LokiDetectedFieldsOut mirrors Loki's modern response (fields + optional limit).
type LokiDetectedFieldsOut struct {
	Fields []DetectedFieldOut `json:"fields"`
	Limit  *int               `json:"limit,omitempty"`
}

// DetectedFieldValue represents one field value and its count
type DetectedFieldValue struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

// LokiDetectedFieldValuesResponse represents detected_field/{name}/values response
// We keep "field" for output to be stable w/ your router param, but accept upstream "label".
type LokiDetectedFieldValuesResponse struct {
	Field  string               `json:"field"`
	Values []DetectedFieldValue `json:"values"`
}

// ===================== Input variants we accept =====================

// Variant A (your cluster shows this)
type detectedFieldsInA struct {
	Fields []struct {
		Label       string   `json:"label"`
		Type        string   `json:"type"`
		Cardinality int      `json:"cardinality"`
		Parsers     []string `json:"parsers"`
	} `json:"fields"`
	Limit *int `json:"limit,omitempty"`
}

// Variant B (alternative/older shape)
type detectedFieldsInB struct {
	DetectedFields []struct {
		// Some builds say "field", others might say "label"
		Field       string `json:"field"`
		Label       string `json:"label"`
		Cardinality int    `json:"cardinality"`
	} `json:"detectedFields"`
}

// Values input variant(s)
type detectedFieldValuesIn struct {
	// upstream may return "field" or "label"—we read either
	Field  string `json:"field"`
	Label  string `json:"label"`
	Values []struct {
		Value string `json:"value"`
		Count int    `json:"count"`
	} `json:"values"`
}

// dfAcc accumulates cardinality, first non-empty type, and a set of parsers.
type dfAcc struct {
	card int
	typ  string
	pset map[string]struct{}
}

func addDetectedField(merged map[string]*dfAcc, label, typ string, cardinality int, parsers []string) {
	if label == "" {
		return
	}
	m, ok := merged[label]
	if !ok {
		m = &dfAcc{pset: make(map[string]struct{})}
		merged[label] = m
	}
	m.card += cardinality
	if m.typ == "" && typ != "" {
		m.typ = typ
	}
	for _, p := range parsers {
		if p == "" {
			continue
		}
		m.pset[p] = struct{}{}
	}
}

// HandleLokiDetectedFields aggregates detected fields from multiple Loki instances.
// Accepts both "fields" and "detectedFields" input envelopes and emits the "fields" envelope.
func HandleLokiDetectedFields(ctx context.Context, w http.ResponseWriter, results <-chan *http.Response, logger log.Logger) {
	ctx, span := traces.CreateSpan(ctx, "handle_detected_fields")
	defer span.End()

	// merged[label] => accumulator
	merged := make(map[string]*dfAcc)
	var limit *int // keep the first non-nil limit we see

	for resp := range results {
		if resp == nil || resp.Body == nil {
			_, errSpan := traces.CreateSpan(ctx, "detected_fields.nil_response")
			errSpan.RecordError(io.ErrUnexpectedEOF)
			errSpan.SetStatus(codes.Error, "nil upstream response/body")
			if metrics.RequestFailures != nil {
				metrics.RequestFailures.Add(ctx, 1, metric.WithAttributes(
					attribute.String("path", "/loki/api/v1/detected_fields"),
					attribute.String("method", "GET"),
					attribute.String("error_type", "nil_response"),
				))
			}
			errSpan.End()
			level.Warn(logger).Log("msg", "nil response/body for detected_fields")
			continue
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			_, errSpan := traces.CreateSpan(ctx, "detected_fields.read_body")
			errSpan.RecordError(err)
			errSpan.SetStatus(codes.Error, "failed to read response body")
			if metrics.RequestFailures != nil {
				metrics.RequestFailures.Add(ctx, 1, metric.WithAttributes(
					attribute.String("path", "/loki/api/v1/detected_fields"),
					attribute.String("method", "GET"),
					attribute.String("error_type", "read_body_failed"),
				))
			}
			errSpan.End()
			level.Error(logger).Log("msg", "failed to read detected_fields body", "err", err)
			continue
		}
		level.Debug(logger).Log("msg", "received detected_fields body", "body", string(body))

		// Try variant A first
		var a detectedFieldsInA
		if err := json.Unmarshal(body, &a); err == nil && (len(a.Fields) > 0 || a.Limit != nil) {
			for _, f := range a.Fields {
				addDetectedField(merged, f.Label, f.Type, f.Cardinality, f.Parsers)
			}
			// keep first non-nil limit
			if limit == nil && a.Limit != nil {
				limit = a.Limit
			}
			continue
		}

		// Try variant B
		var b detectedFieldsInB
		if err := json.Unmarshal(body, &b); err == nil && len(b.DetectedFields) > 0 {
			for _, f := range b.DetectedFields {
				label := f.Label
				if label == "" {
					label = f.Field
				}
				addDetectedField(merged, label, "", f.Cardinality, nil)
			}
			continue
		}

		// If neither shape matched, ignore this backend (already debug-logged above)
	}

	// Build output
	out := make([]DetectedFieldOut, 0, len(merged))
	for label, a := range merged {
		parsers := make([]string, 0, len(a.pset))
		for p := range a.pset {
			parsers = append(parsers, p)
		}
		sort.Strings(parsers)
		out = append(out, DetectedFieldOut{
			Label:       label,
			Type:        a.typ,
			Cardinality: a.card,
			Parsers:     parsers,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })

	resp := LokiDetectedFieldsOut{Fields: out, Limit: limit}
	w.Header().Set("Content-Type", "application/json")

	_, encSpan := traces.CreateSpan(ctx, "detected_fields.encode_response")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		encSpan.RecordError(err)
		encSpan.SetStatus(codes.Error, "failed to encode detected_fields response")
		level.Error(logger).Log("msg", "failed to encode detected_fields response", "err", err)
	}
	encSpan.End()
}

// HandleLokiDetectedFieldValues aggregates values for a given detected field.
// Accepts upstream envelopes using either "field" or "label" as the name key.
func HandleLokiDetectedFieldValues(ctx context.Context, w http.ResponseWriter, results <-chan *http.Response, fieldName string, logger log.Logger) {
	ctx, span := traces.CreateSpan(ctx, "handle_detected_field_values")
	defer span.End()

	merged := make(map[string]int)

	for resp := range results {
		if resp == nil || resp.Body == nil {
			_, errSpan := traces.CreateSpan(ctx, "detected_field_values.nil_response")
			errSpan.RecordError(io.ErrUnexpectedEOF)
			errSpan.SetStatus(codes.Error, "nil upstream response/body")
			if metrics.RequestFailures != nil {
				metrics.RequestFailures.Add(ctx, 1, metric.WithAttributes(
					attribute.String("path", "/loki/api/v1/detected_field/{name}/values"),
					attribute.String("method", "GET"),
					attribute.String("error_type", "nil_response"),
				))
			}
			errSpan.End()
			level.Warn(logger).Log("msg", "nil response/body for detected_field values")
			continue
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			_, errSpan := traces.CreateSpan(ctx, "detected_field_values.read_body")
			errSpan.RecordError(err)
			errSpan.SetStatus(codes.Error, "failed to read response body")
			if metrics.RequestFailures != nil {
				metrics.RequestFailures.Add(ctx, 1, metric.WithAttributes(
					attribute.String("path", "/loki/api/v1/detected_field/{name}/values"),
					attribute.String("method", "GET"),
					attribute.String("error_type", "read_body_failed"),
				))
			}
			errSpan.End()
			level.Error(logger).Log("msg", "failed to read detected_field values body", "err", err)
			continue
		}
		level.Debug(logger).Log("msg", "received detected_field values body", "body", string(body))

		var in detectedFieldValuesIn
		if err := json.Unmarshal(body, &in); err != nil {
			_, errSpan := traces.CreateSpan(ctx, "detected_field_values.unmarshal")
			errSpan.RecordError(err)
			errSpan.SetStatus(codes.Error, "failed to unmarshal detected_field values")
			if metrics.RequestFailures != nil {
				metrics.RequestFailures.Add(ctx, 1, metric.WithAttributes(
					attribute.String("path", "/loki/api/v1/detected_field/{name}/values"),
					attribute.String("method", "GET"),
					attribute.String("error_type", "json_unmarshal_failed"),
				))
			}
			errSpan.End()
			level.Error(logger).Log("msg", "failed to unmarshal detected_field values", "err", err)
			continue
		}

		// NOTE: We ignore in.Field/Label for the name; we trust the router param (fieldName).
		for _, v := range in.Values {
			merged[v.Value] += v.Count
		}
	}

	final := make([]DetectedFieldValue, 0, len(merged))
	for val, cnt := range merged {
		final = append(final, DetectedFieldValue{Value: val, Count: cnt})
	}
	sort.Slice(final, func(i, j int) bool { return final[i].Value < final[j].Value })

	resp := LokiDetectedFieldValuesResponse{Field: fieldName, Values: final}
	w.Header().Set("Content-Type", "application/json")

	_, encSpan := traces.CreateSpan(ctx, "detected_field_values.encode_response")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		encSpan.RecordError(err)
		encSpan.SetStatus(codes.Error, "failed to encode detected_field values response")
		level.Error(logger).Log("msg", "failed to encode detected_field values response", "err", err)
	}
	encSpan.End()
}
