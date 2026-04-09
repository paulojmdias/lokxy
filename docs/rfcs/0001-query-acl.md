# RFC 0001: Dynamic Query ACL

| Field       | Value         |
|-------------|---------------|
| Status      | Draft         |
| Author      | paulojmdias   |
| Created     | 2026-04-09    |

## Overview

Lokxy is a fan-out proxy for Loki. Every query hitting Lokxy is forwarded to every
configured backend with no filtering, transformation, or access control. Security today
depends entirely on the network layer.

This RFC proposes a **query policy engine** -- a middleware that intercepts Loki queries,
evaluates operator-defined rules against LogQL stream selectors and HTTP headers, and
enforces or warns before forwarding. The goal is to educate users into writing well-scoped,
efficient queries, not to block them arbitrarily.

### Prior art: Loki's built-in query blocking

Loki itself supports [blocking unwanted queries][loki-blocking] via per-tenant runtime
overrides. That mechanism operates server-side and requires access to Loki's configuration
(or its runtime overrides file). It matches queries by pattern, regex, hash, query type,
and `X-Query-Tags` headers.

Lokxy's ACL serves a different use case:

- **Proxy-side enforcement.** Operators who do not control Loki's configuration (shared
  clusters, managed services) can still enforce query policies at the proxy layer.
- **Structural rules.** Rather than matching query strings by pattern/regex, Lokxy ACL
  reasons about LogQL stream selectors semantically -- it can require specific labels,
  detect empty or catch-all selectors, and inject default matchers into the AST.
- **Fan-out protection.** Lokxy forwards every query to every backend simultaneously. A
  broad query that would be expensive on one Loki instance becomes N times as expensive
  through a fan-out proxy. ACL intercepts before fan-out.
- **Warn mode.** Rules can run in `warn` mode to observe impact before enforcing, with
  metrics and response headers for visibility.

The two mechanisms are complementary. Loki's blocking is the last line of defence on the
server; Lokxy's ACL is the first line of defence at the proxy entry point.

## Motivation

Without query-level enforcement, any user with network access to Lokxy can:

1. Run `{app=~".*"}` and trigger a full index scan across all backends simultaneously.
2. Omit required labels (e.g. `namespace`) when querying high-cardinality services, causing
   backends to scan orders of magnitude more data than intended.
3. Accidentally query production logs from automation or scripts with broad selectors.

These are **operational** problems, not malicious attacks. The ACL engine is designed as a
guardrail: it tells users what is wrong with their query and how to fix it. Rules are
rollout-safe via an `enforcement` field (`enforce` vs `warn`) that lets operators introduce
rules gradually and observe their impact before they start rejecting queries.

## Scope

### In scope

- Intercepting LogQL queries at the HTTP layer before fan-out.
- Evaluating operator-defined rules against stream selectors and HTTP headers.
- Four rule actions: `allow`, `block`, `require_matcher`, `inject_matcher`.
- Observable decisions: Prometheus metrics, structured logs, response headers.
- Warn mode for safe rollout.

### Out of scope

- Authentication or identity management. ACL operates on HTTP headers provided by an
  upstream identity-aware proxy (e.g. `X-Forwarded-User`). Lokxy does not verify identity.
- Per-backend routing or selective fan-out. Lokxy sends every query to every backend;
  ACL does not change this. Routing is a separate concern.
- Rate limiting or quota enforcement. ACL evaluates query shape, not volume.
- Log content filtering or response redaction.

## Background

### Lokxy architecture

Lokxy is a pure fan-out proxy built on Go's `net/http` stdlib. The request path is:

```
Client -> HTTPTracesHandler -> NewServeMux -> proxyHandler (inner mux)
       -> per-route handlers -> fanoutRequest -> all backends
```

Key characteristics relevant to ACL:

- **No middleware framework.** Uses `http.ServeMux` with `http.Handler` and
  `http.HandlerFunc`. Middleware must be wired manually.
- **`proxyHandler` returns `func(http.ResponseWriter, *http.Request)`**, not `http.Handler`.
  An adapter is needed to wrap it with standard middleware.
- **POST body is consumed** by `fanoutRequest` via `io.ReadAll`. Any middleware reading
  `r.Body` must buffer and restore it.
- **WebSocket tail** passes through HTTP handler layers before upgrade. Middleware can
  intercept before the WebSocket handshake.

### LogQL syntax package

Lokxy already depends on Loki v3.7.1. The `logql/syntax` package provides:

| Need | API |
|---|---|
| Parse any query | `syntax.ParseExprWithoutValidation(q)` |
| Extract matchers | `syntax.MatcherGroups(expr)` |
| Walk AST | `expr.Walk(fn)` |
| Append matchers | `(*MatchersExpr).AppendMatchers(...)` |
| Clone before mutation | `syntax.MustClone[syntax.Expr](expr)` |
| Re-serialize | `expr.String()` |

`syntax.ParseExpr` must NOT be used -- it rejects broad queries like `{app=~".*"}`, which
is exactly what ACL needs to catch. `ParseExprWithoutValidation` parses without Loki's
own validation, deferring policy to the ACL engine.

## Design

### Evaluation model

Rules are evaluated in a **single pass, top-to-bottom in declaration order**. The action
type of the first matching rule determines the outcome:

```
for each rule in declaration order:
  if rule.when matches:
    switch rule.action:
      allow            -> short-circuit: skip all remaining rules, forward query as-is
      block            -> short-circuit: reject with 400 and rule's reason
      require_matcher  -> check require list; if violated, accumulate violation
      inject_matcher   -> record injection (applied after all rules are evaluated)

after all rules:
  -> if any require_matcher violations accumulated -> return 400 listing ALL missing labels
  -> otherwise -> apply recorded injections in declaration order, forward query
```

Key properties:

1. **`allow` genuinely overrides everything.** An `allow` rule before blocks and requirements
   acts as an escape hatch (e.g. `sre-bot` bypasses all restrictions).
2. **`block` short-circuits immediately.** No further rules are evaluated.
3. **`require_matcher` violations accumulate.** The user sees every missing label in a
   single error response, not through trial-and-error.
4. **`inject_matcher` is deferred.** Policy decisions are always made against the user's
   original query. Injection is an operational convenience that happens transparently before
   forwarding. This prevents injections from silently satisfying requirements.
5. **Rule ordering is the operator's responsibility.** The engine does not re-sort, group,
   or prioritize rules by action type.

#### Alternative: grouped evaluation

An alternative design groups rules by action type (evaluate all `allow` first, then all
`block`, then all `require_matcher`, then all `inject_matcher`). This was rejected because:

- It removes operator control over precedence between action types.
- It creates surprising interactions when an `allow` rule is intended to override a specific
  `block` but the grouping causes all blocks to run before any allows.
- Declaration-order is simpler to reason about and debug.

### `when` condition matching

The `when` block defines when a rule fires. A rule with no `when` fires on every query.
All conditions in a `when` list must match (AND logic).

Conditions can match against:

- **Query matchers** (`source: "query"`, default): checks LogQL stream selector labels.
  For `=~` matchers, the user's regex is compiled and tested against the condition's `value`.
- **HTTP headers** (`source: "header"`): checks request headers by name. Uses simple
  string equality.
- **Empty selector** (`empty_selector: true`): fires when the query has no meaningful
  stream selector -- either literally empty (`{}`) or when all matchers are catch-all
  patterns (`=~".*"`, `=~".+"`, `!=""`, and non-greedy variants).

### Enforcement levels

| Value | Behaviour |
|---|---|
| `enforce` | Reject the query with HTTP 400 |
| `warn` | Forward the query; add `X-Lokxy-Policy-Warning` response header; emit structured log |

All new rules should start in `warn` mode. This is conceptually similar to Loki's own
approach where [blocked queries are logged and counted][loki-blocking] via the
`loki_blocked_queries` metric, giving operators visibility before enforcement.

### Error format

Errors use Loki's own JSON format so Grafana surfaces them inline:

```json
{"status": "error", "error": "<reason text>"}
```

### Endpoint classification

ACL only intercepts endpoints that carry a LogQL expression and return log content or
execute arbitrary queries. Metadata and discovery endpoints pass through.

**Enforced:**

| Endpoint | Query param |
|---|---|
| `/loki/api/v1/query` | `query` |
| `/loki/api/v1/query_range` | `query` |
| `/loki/api/v1/tail` | `expr` |
| `/loki/api/v1/series` | `match[]` |
| `/loki/api/v1/index/stats` | `query` |

**Passthrough (no ACL evaluation):**

| Endpoint | Reason |
|---|---|
| `/loki/api/v1/labels` | No query param; returns label names only |
| `/loki/api/v1/label/{name}/values` | No query param; returns label values only |
| `/loki/api/v1/index/volume` | Metadata aggregation; used by Grafana Logs Drilldown |
| `/loki/api/v1/index/volume_range` | Metadata aggregation |
| `/loki/api/v1/patterns` | Log pattern detection metadata |
| `/loki/api/v1/detected_labels` | Label discovery metadata |
| `/loki/api/v1/detected_fields` | Field discovery metadata |

**Rationale for excluding Logs Drilldown endpoints:** These 4 endpoints
(`/index/volume`, `/patterns`, `/detected_labels`, `/detected_fields`) accept `query`
parameters but return only metadata -- not log content. Enforcing ACL on them would break
Grafana's Logs Drilldown plugin, which sends broad queries like `{service_name=~".+"}` on
its landing page. Since `/labels` already returns label names without ACL, these endpoints
do not widen the information boundary.

## Configuration

ACL rules live inside the existing `config.yaml` under an `acl:` top-level key. No
separate file is required.

### Schema

```yaml
acl:
  enabled: true
  default_action: allow   # allow | block -- applies when no rule matches
  default_reason: ""      # message when default_action is block and no rule matches
                          # defaults to "Query rejected by default policy" if omitted

  rules:
    # Escape hatches first -- bypass everything below
    - name: "allow-internal-bot"
      action: allow
      when:
        - name: "X-Forwarded-User"
          value: "sre-bot"
          source: header

    # Hard blocks
    - name: "block-empty-selector"
      action: block
      enforcement: enforce
      reason: "Queries must include at least one stream selector"
      when:
        - empty_selector: true

    # Conditional requirements
    - name: "require-namespace-for-payments"
      action: require_matcher
      enforcement: enforce
      reason: >
        Queries targeting the payments service must also specify namespace.
        Example: {service="payments", namespace="prod"}
      when:
        - name: "service"
          value: "payments"
          types: ["=", "=~"]
      require:
        - name: "namespace"
          types: ["=", "=~"]

    # Injections (applied after policy evaluation)
    - name: "inject-default-cluster"
      action: inject_matcher
      inject:
        name: "cluster"
        value: "eu-prod"
        type: "="
        if_absent: true
```

### Rule struct

```
Rule
  name          string          -- unique; used in logs and metrics
  action        enum            -- allow | block | require_matcher | inject_matcher
  enforcement   enum            -- enforce (default) | warn
  reason        string          -- user-facing message
  when          []MatchCondition
    name            string      -- label name or header name
    value           string      -- target value
    types           []string    -- restrict to matcher types; empty = any
    source          string      -- "query" (default) | "header"
    absent          bool        -- fires when label is NOT present
    empty_selector  bool        -- fires on empty/catch-all selectors
  require       []RequireSpec   -- for require_matcher
    name            string
    types           []string
  inject        InjectSpec      -- for inject_matcher
    name            string
    value           string
    type            string      -- =, !=, =~, !~
    if_absent       bool
```

## Implementation

### Package layout

```
pkg/acl/
  config.go          -- rule structs, validation
  engine.go          -- single-pass evaluation loop
  logql.go           -- LogQL parsing, matcher extraction, query rewriting
  middleware.go      -- http.Handler middleware
  *_test.go
```

Changes to existing files:

| File | Change |
|---|---|
| `pkg/config/config.go` | Add `ACL ACLConfig` field |
| `pkg/proxy/mux.go` | Wrap handler with `acl.Middleware` when enabled |

### Middleware wiring

`proxyHandler` returns `func(http.ResponseWriter, *http.Request)`, not `http.Handler`.
The adapter pattern:

```go
var h http.Handler = http.HandlerFunc(proxyHandler(cfg, logger))
if cfg.ACL.Enabled {
    h = acl.Middleware(engine, logger)(h)
}
proxyMux.Handle("/", h)
```

### Phased delivery

#### Phase 1: `block` and `require_matcher`

Core enforcement. Operators can block dangerous queries and require label combinations.
The engine implements the single-pass loop but only handles `block` and `require_matcher`.
`allow` and `inject_matcher` are validated but skipped during evaluation.

Deliverables:
- `config.go`: structs, validation, yaml tags (using `gopkg.in/yaml.v2`)
- `logql.go`: query parsing, param extraction (GET + POST), condition matching
- `engine.go`: evaluation loop for block + require_matcher
- `middleware.go`: HTTP middleware with endpoint filtering, POST body buffering
- Config and mux integration
- Unit and integration tests

#### Phase 2: `allow` and `inject_matcher`

Escape hatches and query rewriting. Completes the evaluation loop.

Deliverables:
- `allow` handling: short-circuit, discard accumulated violations
- `inject_matcher` handling: record injections, apply after evaluation via AST manipulation
- `InjectMatcher()`: walk AST, append to every `MatchersExpr`, clone before mutation
- `ReplaceQueryParam()`: rewrite GET URL and POST body
- `X-Lokxy-Policy-Injected` response header for transparency
- Tests verifying injection does not satisfy requirements

### Observability

- **Metric:** `lokxy_acl_decisions_total{rule, action, enforcement}` -- counter per decision.
  The `rule` label is kept because the rule set in practice will be small (tens, not
  hundreds); cardinality is bounded by operator-controlled configuration.
- **Structured log** per decision (both enforce and warn):
  ```json
  {"level":"warn","rule":"require-namespace-for-payments","action":"require_matcher",
   "enforcement":"warn","query":"{service=\"payments\"}","user":"alice"}
  ```
- **Response headers:** `X-Lokxy-Policy-Warning` (warn mode), `X-Lokxy-Policy-Injected`
  (inject transparency)

## Configuration reload

Hot reload of ACL rules without restarting the proxy is desirable but depends on the
broader configuration reload mechanism tracked in
[paulojmdias/lokxy#21](https://github.com/paulojmdias/lokxy/issues/21).

Lokxy is expected to run on Kubernetes, where filesystem notification (`fsnotify`) may not
work reliably with ConfigMap-mounted volumes (kubelet uses symlink swaps that some
`inotify` watchers miss). The reload strategy -- whether `fsnotify`, polling, SIGHUP, or
an HTTP endpoint -- should be decided as part of issue #21 and apply uniformly to all
configuration sections, not just ACL.

Until then, ACL rules are loaded once at startup. Changing rules requires a pod restart
(or rolling deployment).

## Known limitations and edge cases

### KiB byte literal round-trip

Loki's `syntax` package has a known bug where byte unit literals (e.g. `2.5KiB`) do not
survive `String()` -> parse round-trips. If `inject_matcher` rewrites a metric query
containing byte literals, the re-serialized query could be unparseable. Documented as a
known limitation; string-level injection can be used as a fallback.

### Parser input size limit

The LogQL parser rejects inputs >= 128KB. Extremely long queries will fail to parse. The
middleware will either skip ACL with a warning log or return a clear error.

### `empty_selector` heuristic boundaries

The catch-all detection covers common patterns (`=~".*"`, `=~".+"`, `!=""`) but does not
solve regex equivalence in the general case. Exotic catch-all patterns like `=~"[\\s\\S]*"`
will not trigger `empty_selector`. Operators can add explicit `block` rules for these if
needed.

### Multiple matcher groups

A `BinOpExpr` like `rate({app="a"}[5m]) / rate({app="b"}[5m])` contains two independent
stream selectors. All groups are extracted via `syntax.MatcherGroups(expr)` and each must
satisfy rules independently.

### `match[]` multi-value

The `/series` endpoint accepts multiple `match[]` parameters. Each is evaluated
independently; all must pass. This prevents bypassing ACL by splitting a broad query
across multiple values.

## References

- [Loki: Block unwanted queries][loki-blocking] -- Loki's built-in per-tenant query
  blocking via runtime overrides; the server-side complement to proxy-side ACL
- [Loki LogQL documentation](https://grafana.com/docs/loki/latest/query/)
- [Grafana Logs Drilldown plugin](https://grafana.com/docs/grafana/latest/explore/simplified-exploration/logs/)
- [paulojmdias/lokxy#21](https://github.com/paulojmdias/lokxy/issues/21) -- configuration
  reload tracking issue

[loki-blocking]: https://grafana.com/docs/loki/latest/operations/blocking-queries/
