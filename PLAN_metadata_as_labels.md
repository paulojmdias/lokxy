# Feature Plan: Expose Loki Structured Metadata Fields as Labels

## Context

Loki stores high-cardinality data (trace IDs, user IDs, request IDs, etc.) as **structured
metadata fields** rather than stream labels to avoid index explosion. This means those fields
are invisible to tools like Grafana when they browse the label namespace, making them
unavailable for filtering via the standard label selector UI.

lokxy can act as a **transparent wrapper**: when a client queries `/loki/api/v1/labels` or
`/loki/api/v1/label/{name}/values`, it fans out to each backend's `/detected_fields` /
`/detected_field/{name}/values` endpoints in parallel, merges the discovered field
names/values into the label response, and returns the union. From the client's perspective,
those metadata fields look like ordinary labels.

The feature is:
- **Off by default** — zero behaviour change without opt-in
- **Configurable** with an optional field allowlist (empty = all detected fields)
- **Dynamically discovered** — no static list needed; the proxy calls `/detected_fields` on
  each server group only when the label-discovery APIs (`/labels`, `/label/{name}/values`)
  are invoked

---

## Configuration

```yaml
metadata_as_labels:
  enabled: true
  # Optional: only expose these specific fields as labels.
  # If omitted or empty, all detected fields from every server group are exposed.
  fields:
    - trace_id
    - user_id
    - request_id
```

---

## Files Changed

| File | Change |
|------|--------|
| `pkg/config/config.go` | Add `MetadataAsLabelsConfig` struct; add field to `Config` |
| `pkg/proxy/proxy.go` | Add `backgroundFanoutBytes` helper + `isFieldExposed`; update route handlers for `/labels` and `/label/{name}/values` |
| `pkg/proxy/handler/labels.go` | Add `HandleLokiLabelsWithMetadata` and `HandleLokiLabelValuesWithMetadataField` |
| `pkg/proxy/handler/metadata_labels_test.go` | Tests for both new handler functions |
| `mixin/play/lokxy/lokxy.yaml` | Commented-out example of the new config block |

---

## Architecture

### New config struct (`pkg/config/config.go`)

```go
// MetadataAsLabelsConfig controls whether detected structured-metadata fields
// are surfaced as Loki labels in the /labels and /label/{name}/values endpoints.
type MetadataAsLabelsConfig struct {
    // Enabled turns the feature on or off. Default: false.
    Enabled bool `yaml:"enabled"`
    // Fields is an optional allowlist of field names to expose.
    // When empty, all fields returned by /detected_fields are exposed.
    Fields []string `yaml:"fields,omitempty"`
}
```

### Background fanout helper (`pkg/proxy/proxy.go`)

```go
// backgroundFanoutBytes fans out to overridePath on all backends, aggregates
// using fn into a buffer, and returns a channel that delivers the body bytes
// when done. It runs concurrently with the caller's own fanoutRequest.
//
// The original request's query string (start=, end=, query=, limit=, etc.) is
// preserved so that field discovery is scoped to the same time range and log
// stream selector as the incoming label request.
func (p *proxy) backgroundFanoutBytes(
    ctx context.Context,
    r *http.Request,
    overridePath string,
    fn transformFn,
) <-chan []byte
```

### Handler functions (`pkg/proxy/handler/labels.go`)

**`HandleLokiLabelsWithMetadata`** — augments `/labels` responses:
1. Drain normal label results from all backends (same as `HandleLokiLabels`)
2. Unmarshal pre-aggregated `/detected_fields` bytes; add each field name to the label set
   (filtered by `allowedFields` allowlist)
3. Return deduplicated, sorted `{"status":"success","data":[...]}` response

**`HandleLokiLabelValuesWithMetadataField`** — augments `/label/{name}/values` responses:
1. Drain normal label-values results from all backends
2. Unmarshal pre-aggregated `/detected_field/{name}/values` bytes; add each value
3. Return deduplicated, sorted `{"status":"success","data":[...]}` response

---

## Concurrent Fanout Flow

```
Client → GET /loki/api/v1/labels?query={job="x"}&start=...&end=...
            │
            ├── backgroundFanoutBytes (goroutine) ──→ GET /detected_fields?query={job="x"}&start=...&end=...
            │   (path overridden, RawQuery kept)        on all backends → HandleLokiDetectedFields → bytes
            │
            └── fanoutRequest (foreground) ─────────→ GET /labels?query={job="x"}&start=...&end=...
                    └── HandleLokiLabelsWithMetadata(labelsResults, <-fieldsCh, allowedFields)


Client → GET /loki/api/v1/label/trace_id/values?query={job="x"}&start=...&end=...
            │
            ├── backgroundFanoutBytes (goroutine) ──→ GET /detected_field/trace_id/values?query={job="x"}&start=...&end=...
            │   (path overridden, RawQuery kept)        on all backends → HandleLokiDetectedFieldValues → bytes
            │
            └── fanoutRequest (foreground) ─────────→ GET /label/trace_id/values?query={job="x"}&start=...&end=...
                    └── HandleLokiLabelValuesWithMetadataField(labelValuesResults, <-fieldValuesCh)
```

Both fanouts hit the same backends in parallel. The handler only blocks on `<-fieldsCh` if
the detected_fields fanout is still in flight when the labels fanout completes. If the
detected_fields fanout fails entirely, the handler receives empty/error bytes, falls back
to real labels only, and degrades gracefully.

---

## Verification

```bash
# Unit tests
go test ./...

# Integration smoke test with metadata_as_labels.enabled: true
# 1. GET /loki/api/v1/labels
#    → response includes metadata field names alongside real stream labels
# 2. GET /loki/api/v1/label/trace_id/values
#    → returns values from /detected_field/trace_id/values on the backends
# 3. Same requests with metadata_as_labels.enabled: false
#    → unchanged output (feature is off)
# 4. With fields: [trace_id] allowlist
#    → only trace_id appears; other detected fields excluded
```
