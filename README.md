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

- Go (v1.23+)
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
docker run --rm -it -p 8080:8080 -v $(pwd)/config.yaml:/lokxy/config.yaml lokxy:latest lokxy --config /lokxy/config.yaml
```

This command binds the container to port 8080 and mounts the local config.yaml file for configuration. Adjust ports and file paths as needed.

## Play with Lokxy

We've provide a `docker-compose.yml` file located in the mixin/play/ folder to help you quickly get Lokxy up and running using Docker.

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

  - name: "Loki 3"
    url: "https://localhost:3102"
    timeout: 60
    headers:
      Authorization: "Basic <token>"
      X-Scope-OrgID: org3
    http_client_config:
      tls_config:
        insecure_skip_verify: true

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
    * `http_client_config`: HTTP Client custom configurations
        * `dial_timeout`: Timeout duration for establishing a connection. Defaults to 200ms.
        * `tls_config`:
            * `insecure_skip_verify`: If set to true, the client will not verify the server’s certificate chain or host name.
            * `ca_file`: Path to a custom Certificate Authority (CA) certificate file to verify the server.
            * `cert_file`: Path to the client certificate file for mutual TLS.
            * `key_file`: Path to the client key file for mutual TLS.

* `logging`:
    * `level`: Defines the log level (`debug`, `info`, `warn`, `error`).
    * `format`: The log output format, either in `json` or `logfmt`.

## Usage

Once `lokxy` is running, you can query Loki instances by sending HTTP requests to the proxy.

The following APIs are supported:
* Querying Logs: `/loki/api/v1/query`
* Querying Range: `/loki/api/v1/query_range`
* Series API: `/loki/api/v1/series`
* Index Stats API: `/loki/api/v1/index/stats`
* Labels API: `/loki/api/v1/labels`
* Label Values API: `/loki/api/v1/label/{label_name}/values`
* Tailing Logs via WebSocket: `/loki/api/v1/tail`

### Example Query:

```bash
curl "http://localhost:3100/loki/api/v1/query?query={job=\"myapp\"}"
```

Logs from all configured Loki instances will be aggregated and returned.
