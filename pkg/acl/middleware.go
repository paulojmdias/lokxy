package acl

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/paulojmdias/lokxy/pkg/o11y/metrics"
)

// warningHeader carries the name of each warn-mode rule that matched, for
// visibility during rollout before a rule is switched to enforce.
const warningHeader = "X-Lokxy-Policy-Warning"

// Middleware returns HTTP middleware that evaluates ACL rules against Loki
// query endpoints before the request is forwarded. getEngine is called per
// request so that the active engine tracks configuration reloads; when it
// returns nil (ACL disabled) the request passes through untouched.
func Middleware(getEngine func() *Engine, logger log.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			engine := getEngine()
			if engine == nil {
				next.ServeHTTP(w, r)
				return
			}

			ep, enforced := lookupEndpoint(r.URL.Path)
			if !enforced {
				next.ServeHTTP(w, r)
				return
			}

			queries, ok := extractQueries(r, ep, logger)
			if !ok || len(queries) == 0 {
				// Nothing to evaluate, or the body could not be read; fail open.
				next.ServeHTTP(w, r)
				return
			}

			selectors, ok := selectorsFor(queries, r, logger)
			if !ok {
				// A query failed to parse; per RFC we fail open with a warning.
				next.ServeHTTP(w, r)
				return
			}

			decision := engine.Evaluate(selectors, r.Header)
			recordDecision(r.Context(), decision)

			if decision.Reject {
				level.Info(logger).Log(
					"msg", "Query rejected by ACL policy",
					"rule", decision.Rule,
					"path", r.URL.Path,
					"reason", decision.Reason,
				)
				writeRejection(w, decision.Reason, logger)
				return
			}

			for _, warn := range decision.Warnings {
				w.Header().Add(warningHeader, warn.Rule)
				level.Warn(logger).Log(
					"msg", "Query matched ACL policy in warn mode",
					"rule", warn.Rule,
					"path", r.URL.Path,
					"reason", warn.Message,
				)
			}

			next.ServeHTTP(w, r)
		})
	}
}

// extractQueries returns the LogQL expression(s) carried by the request for the
// given endpoint. It reads GET query parameters and POST form bodies, restoring
// r.Body afterwards so the downstream fan-out can re-read it. The bool result is
// false only when the body could not be read (the caller then fails open).
func extractQueries(r *http.Request, ep endpoint, logger log.Logger) ([]string, bool) {
	var bodyBytes []byte
	if r.Body != nil {
		var err error
		bodyBytes, err = io.ReadAll(r.Body)
		if err != nil {
			level.Warn(logger).Log("msg", "ACL failed to read request body; skipping enforcement", "err", err)
			return nil, false
		}
		r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	}

	// ParseForm merges URL query and (for POST url-encoded bodies) the form
	// body into r.Form. It consumes r.Body, so restore it again afterwards.
	_ = r.ParseForm()
	if bodyBytes != nil {
		r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	}

	var out []string
	for _, v := range r.Form[ep.param] {
		if v == "" {
			continue
		}
		out = append(out, v)
		if !ep.multi {
			break
		}
	}
	return out, true
}

// selectorsFor parses every query string into its stream selectors. It returns
// false if any query fails to parse, signaling the caller to fail open.
func selectorsFor(queries []string, r *http.Request, logger log.Logger) ([]selector, bool) {
	var selectors []selector
	for _, q := range queries {
		sels, err := parseSelectors(q)
		if err != nil {
			level.Warn(logger).Log(
				"msg", "ACL could not parse LogQL query; skipping enforcement",
				"path", r.URL.Path,
				"query", q,
				"err", err,
			)
			return nil, false
		}
		selectors = append(selectors, sels...)
	}
	return selectors, true
}

// writeRejection emits Loki's error JSON so Grafana surfaces the reason inline.
func writeRejection(w http.ResponseWriter, reason string, logger log.Logger) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	body := map[string]string{"status": "error", "error": reason}
	if err := json.NewEncoder(w).Encode(body); err != nil {
		level.Error(logger).Log("msg", "Failed to write ACL rejection response", "err", err)
	}
}

// recordDecision increments the ACL decision counter once per policy event.
func recordDecision(ctx context.Context, decision Decision) {
	for _, ev := range decision.Events {
		metrics.ACLDecisions.Add(ctx, 1, metric.WithAttributes(
			attribute.String("rule", ev.Rule),
			attribute.String("action", string(ev.Action)),
			attribute.String("enforcement", string(ev.Enforcement)),
			attribute.String("decision", ev.Outcome),
		))
	}
}
