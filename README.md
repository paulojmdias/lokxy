# Lokxy
Lokxy is a powerful log aggregator for Loki, designed to collect and unify log streams from multiple sources into a single, queryable endpoint. It simplifies log management and enhances visibility across distributed environments, providing seamless integration with your existing Loki infrastructure.

## Table of Contents
- [Motivation & Inspiration](#motivation-and-inspiration)
- [Requirements](#requirements)
- [Installation](#installation)
- [How to Run Locally](#how-to-run-locally)
- [How to Run as a Container](#how-to-run-as-a-container)
- [Play with Lokxy](#play-with-lokxy)
- [Configuration File](#configuration-file)
- [Usage](#usage)

---

## Motivation and Inspiration

**Lokxy** addresses the increasing complexity in observability workflows, especially in large-scale, distributed environments where log management across multiple instances becomes a challenge. Inspired by the design philosophy of **Promxy**, Lokxy provides a similar proxy-based solution but focused on log aggregation for **Loki**.

With **Loki** being a powerful log aggregation tool, Lokxy leverages it as a backend to enable users to seamlessly aggregate and query logs from multiple Loki instances. This approach is designed to simplify querying, enhance observability, and improve scalability in environments where managing logs across several backends can become inefficient.

We draw particular inspiration from **[Promxy](https://github.com/jacksontj/promxy)** for Prometheus, which bridges multiple backends into a single queryable interface. Lokxy replicates this powerful concept for logs, ensuring users have a unified interface to query, without needing to directly interact with each individual Loki instance.

---

## Requirements
Before running **lokxy**, ensure the following are installed:

- Go (v1.26+)
- Docker (if running as a container)
- Make (for running build scripts)

---

## Installation

Clone the repository to get started:

```bash
git clone https://github.com/paulojmdias/lokxy.git
cd lokxy
```

You can install dependencies and build the project using:

```bash
go mod tidy
go build -o lokxy ./cmd/
```

## How to Run Locally

To run lokxy locally, use the following steps:

1. Prepare your configuration file:
Ensure you have a config.yaml file in your working directory (or provide its path during startup). See Configuration File for details.

2. Run the proxy:

```bash
go run cmd/main.go
```

Alternatively, after building the binary, run:

```bash
./lokxy --config config.yaml
```

The application will start serving at the specified port as defined in your config.yaml.


## How to Run as a Container

```bash
docker run --rm -it -p 3100:3100 -v $(pwd)/config.yaml:/lokxy/config.yaml lokxy:latest lokxy --config /lokxy/config.yaml
```

This command binds the container to port 3100 and mounts the local config.yaml file for configuration. Adjust ports and file paths as needed.

## Play with Lokxy

We provide a `docker-compose.yml` file located in the mixin/play/ folder to help you quickly get Lokxy up and running using Docker.

1. Navigate to the mixin/play directory
```sh
cd mixin/play/
```

2. Start Lokxy with Docker Compose
```sh
docker-compose up
```

This will start 2 isolated Loki instances, 2 Promtail instances, 1 Lokxy and 1 Grafana. When it's up and running, you just need to open Grafana in http://localhost:3000, and see the data on Explore menu.

You will found 3 different data sources on Grafana:
* `loki1` -> Instance number 1 from Loki
* `loki2` -> Instance number 2 from Loki
* `lokxy` -> Datasource which will aggregate the data from both Loki instances

## Configuration File

The `config.yaml` file defines how lokxy behaves, including details of the Loki instances to aggregate and logging options. Below are the available configuration options:

Example `config.yaml`:

```yaml
server_groups:
  - name: "Loki 1"
    url: "http://localhost:3100"
    timeout: 30
    headers:
      Authorization: "Bearer <token>"
      X-Scope-OrgID: org1

  - name: "Loki 2"
    url: "http://localhost:3101"
    timeout: 60
    headers:
      Authorization: "Bearer <token>"
      X-Scope-OrgID: org2
    # Optional group: if it fails the query still succeeds with partial results
    # (no warning surfaced).
    ignore_error: true

  - name: "Loki 3"
    url: "https://localhost:3102"
    timeout: 60
    headers:
      Authorization: "Basic <token>"
      X-Scope-OrgID: org3
    http_client_config:
      tls_config:
        insecure_skip_verify: true
      transport:
        disable_keep_alives: false
        max_idle_conns: 200
        max_idle_conns_per_host: 50
        idle_conn_timeout: 120s
        expect_continue_timeout: 2s
        response_header_timeout: 25s
        force_attempt_http2: true

logging:
  level: "info"       # Available options: "debug", "info", "warn", "error"
  format: "json"      # Available options: "json", "logfmt"
```

### Configuration Options:

* `server_groups`:
    * `name`: A human-readable name for the Loki instance.
    * `url`: The base URL of the Loki instance.
    * `timeout`: Timeout for requests in seconds.
    * `headers`: Custom headers to include in each request, such as authentication tokens.
    * `ignore_error`: When `true`, this server group's response is optional — see [Error Handling and Partial Results](#error-handling-and-partial-results). Default: `false`.
    * `downgrade_error`: When `true`, this server group's errors are surfaced as warnings instead of failing the query — see [Error Handling and Partial Results](#error-handling-and-partial-results). Default: `false`. Mutually exclusive with `ignore_error`.
    * `http_client_config`: HTTP Client custom configurations
        * `dial_timeout`: Timeout duration for establishing a connection. Defaults to 200ms.
        * `tls_config`:
            * `insecure_skip_verify`: If set to true, the client will not verify the server's certificate chain or host name.
            * `ca_file`: Path to a custom Certificate Authority (CA) certificate file to verify the server.
            * `cert_file`: Path to the client certificate file for mutual TLS.
            * `key_file`: Path to the client key file for mutual TLS.
        * `transport`: HTTP transport tuning parameters. All fields are optional; sensible defaults are applied when omitted.
            * `disable_keep_alives`: Disables HTTP keep-alive connections when set to true. Default: `false`.
            * `max_idle_conns`: Maximum number of idle connections across all hosts. Default: `100`.
            * `max_idle_conns_per_host`: Maximum number of idle connections per host. Default: `20`.
            * `idle_conn_timeout`: Duration an idle connection remains open before being closed. Default: `90s`.
            * `expect_continue_timeout`: Timeout for waiting for a server's "100 Continue" response. Default: `1s`.
            * `response_header_timeout`: Time to wait for a server's response headers after fully writing the request. Does not include response body read time. Default: value of `timeout` (server group timeout); `0` if `timeout` is also unset (no timeout).
            * `force_attempt_http2`: Forces HTTP/2 negotiation even when using custom TLS or dial functions. Default: `true`.

* `logging`:
    * `level`: Defines the log level (`debug`, `info`, `warn`, `error`).
    * `format`: The log output format, either in `json` or `logfmt`.

### Error Handling and Partial Results

By default lokxy treats every server group as **required**: if any group returns an
error, the whole query fails. This protects result consistency but hurts availability
when querying multiple Loki clusters where one may be down for maintenance, degraded,
or simply optional (e.g. an archive tier).

Two per-server-group options let you trade strict consistency for availability:

* `ignore_error: true` — makes the group's response **optional**. If it fails while at
  least one other group succeeds, the query still returns `200` with partial results
  merged from the healthy groups. The failure is **not** surfaced to the client (only
  logged at debug level and counted in the `lokxy_request_degraded_total` metric).

* `downgrade_error: true` — same partial-results behaviour, but the group's error is
  **converted into a warning** rather than being silent. For `query`/`query_range` the
  warning is added to the native Loki `warnings[]` field of the response, which clients
  such as Grafana render as a panel warning. For other endpoints (labels, series,
  stats, volume, patterns, detected_*) there is no native warnings field, so the
  downgrade is surfaced via a warn-level log and the `lokxy_request_degraded_total`
  metric only.

The two options are **mutually exclusive** — setting both on the same group is a
configuration error.

Notes:

* A **required** group failing still fails the entire query (default behaviour,
  unchanged).
* If **every** contributing group is optional and they **all** fail, lokxy forwards the
  last upstream error (e.g. `502`/the upstream status) rather than returning a
  misleading empty `200` — partial results require at least one successful backend.
* These options apply to the aggregated HTTP query endpoints. The live tail
  (`/loki/api/v1/tail`, WebSocket) is inherently best-effort per backend — a backend
  that fails to connect is skipped while the others keep streaming — so it ignores
  these flags.

Example:

```yaml
server_groups:
  - name: primary-cluster
    url: http://loki-primary:3100
    # Required: this cluster must respond successfully.

  - name: archive-cluster
    url: http://loki-archive:3100
    ignore_error: true     # Optional: queries succeed even if it fails (silent).

  - name: secondary-cluster
    url: http://loki-secondary:3100
    downgrade_error: true  # Optional: failures become warnings on the response.
```

### Tracing Configuration

The application includes tracing instrumentation using OpenTelemetry. To collect traces, deploy an OpenTelemetry Collector or compatible tracing backend such as Jaeger, Grafana Tempo, or Zipkin.

Configure the trace export destination using standard OpenTelemetry environment variables: `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` for the collector endpoint, or `OTEL_EXPORTER_OTLP_ENDPOINT` as a fallback. Set `OTEL_EXPORTER_OTLP_INSECURE=true` for development environments using insecure gRPC connections. If no endpoint is configured, the application defaults to `localhost:4317` as per [otlp exporter documentation](https://opentelemetry.io/docs/languages/sdk-configuration/otlp-exporter/). An example of this can also be fund in `mixin/play/`.

## Usage

Once `lokxy` is running, you can query Loki instances by sending HTTP requests to the proxy.

The following APIs are supported:
* Querying Logs: `/loki/api/v1/query`
* Querying Range: `/loki/api/v1/query_range`
* Series API: `/loki/api/v1/series`
* Index Stats API: `/loki/api/v1/index/stats`
* Index Volume API: `/loki/api/v1/index/volume`
* Index Volume Range API: `/loki/api/v1/index/volume`
* Detected Labels API: `/loki/api/v1/detected_labels`
* Labels API: `/loki/api/v1/labels`
* Label Values API: `/loki/api/v1/label/{label_name}/values`
* Detected Fields API: `/loki/api/v1/detected_fields`
* Detected Field Values API: `/loki/api/v1/detected_field/{field_name}/values`
* Patterns API: `/loki/api/v1/patterns`
* Tailing Logs via WebSocket: `/loki/api/v1/tail`

### Example Query:

```bash
curl "http://localhost:3100/loki/api/v1/query?query={job=\"myapp\"}"
```

Logs from all configured Loki instances will be aggregated and returned.

## Star History

[![Star History Chart](https://api.star-history.com/svg?repos=paulojmdias/lokxy&type=Date)](https://www.star-history.com/#paulojmdias/lokxy&Date)
