# haproxy-otel-spoe

A [SPOE (Stream Processing Offload Engine)](https://www.haproxy.com/blog/extending-haproxy-with-the-stream-processing-offload-engine) agent that adds distributed tracing via OpenTelemetry to HAProxy **HTTP frontends**. It creates a server-side span for every HTTP request/response pair and exports them over gRPC OTLP to Tempo or any compatible backend.

> **Limitation:** this agent only works with HAProxy frontends running in `mode http`. TCP frontends (`mode tcp`) do not expose HTTP-level events to SPOE and are not supported.

HAProxy has no native OTLP export — even in the latest 3.4 release. The legacy OpenTracing filter is deprecated and being removed. This agent fills that gap via SPOE, which works on HAProxy 2.8 and later without any HAProxy recompilation or plugins.

## Contents

- [Architecture](#architecture)
- [Requirements](#requirements)
- [Quick start](#quick-start)
- [Configuration](#configuration)
- [HAProxy configuration](#haproxy-configuration)
- [Trace context propagation](#trace-context-propagation)
- [Per-frontend custom attributes](#per-frontend-custom-attributes)
- [Building from source](#building-from-source)
- [Container image](#container-image)
- [Deployment notes](#deployment-notes)

---

## Architecture

```
                  SPOE protocol (TCP)
HAProxy 2.8  ─────────────────────────►  haproxy-otel-spoe agent
  frontend                                       │
  (mode http  ◄──── txn.otel.traceparent ────────┘
   only)                                         │
      │  inject traceparent header               │ gRPC OTLP
      ▼                                          ▼
  upstream service                        Tempo / OTLP backend
```

For each HTTP transaction, HAProxy fires two SPOE messages:

1. **`on-http-request`** — sent when the request arrives at the frontend. The agent opens a new span (or continues an existing trace from an inbound `traceparent` header), stores it in memory keyed by HAProxy's unique request ID, and returns a `traceparent` value via a transaction variable.

2. **`on-http-response`** — sent after the upstream responds. The agent retrieves the open span, records the HTTP status code and any custom attributes, marks the span as error if status >= 500, closes the span, and flushes it to the OTLP exporter.

Spans are buffered in memory and exported asynchronously. If the OTLP endpoint is temporarily unavailable, the exporter retries with exponential backoff and buffers up to 10,000 spans before dropping.

In-flight spans that never receive a response (dropped connections, HAProxy restarts) are automatically evicted and closed after a 30-second TTL.

---

## Requirements

| Component | Minimum version |
|---|---|
| HAProxy | 2.8+ (tested up to 3.4) |
| Go (to build from source) | 1.26 |
| OTLP gRPC backend | Tempo 2.x, Jaeger, or any OTLP-compatible collector |

---

## Quick start

### 1. Start the agent

```bash
OTLP_ENDPOINT=tempo.internal:4317 \
SERVICE_NAME=haproxy \
SPOE_ADDR=0.0.0.0:12345 \
./haproxy-otel-spoe
```

### 2. Install the SPOE config

Copy `haproxy/spoe-otel.cfg` to `/etc/haproxy/spoe-otel.cfg` on each HAProxy node (or wherever HAProxy can read it).

### 3. Configure HAProxy frontends

Add the following to each frontend you want to trace. See the [HAProxy configuration](#haproxy-configuration) section for a full annotated example.

```
unique-id-format %[uuid()]
unique-id-header X-Request-ID

filter spoe engine otel-agent config /etc/haproxy/spoe-otel.cfg

http-request set-header traceparent %[var(txn.otel.traceparent)] \
  if { var(txn.otel.traceparent) -m found }
```

---

## Configuration

All configuration is via environment variables. There are no config files for the agent itself.

| Variable | Default | Description |
|---|---|---|
| `OTLP_ENDPOINT` | `localhost:4317` | gRPC OTLP endpoint (`host:port`). No scheme prefix — the connection is plain TCP by default. |
| `SERVICE_NAME` | `haproxy` | Value of the `service.name` resource attribute attached to every span. |
| `SPOE_ADDR` | `0.0.0.0:12345` | Address the agent listens on for incoming SPOE connections from HAProxy. |
| `OTLP_TLS` | `disabled` | TLS mode for the gRPC connection. Accepted values: `disabled`, `enabled`, `skip-verify`. |

### TLS modes

| Value | Behaviour |
|---|---|
| `disabled` | Plain TCP, no encryption. Suitable for co-located agent and collector. |
| `enabled` | TLS with full certificate verification against the system trust store. |
| `skip-verify` | TLS encryption without certificate verification. Use only in controlled environments. |

---

## HAProxy configuration

Two files are involved: the main `haproxy.cfg` and the SPOE filter config `spoe-otel.cfg`.

### spoe-otel.cfg

This file is fixed — it should not normally be changed. It defines the SPOE agent binding, the two messages sent to the agent, and the variables the agent can set in response.

```ini
[otel-agent]

spoe-agent otel-agent
    messages    on-http-request on-http-response
    option      var-prefix      otel
    option      set-on-error    status
    timeout     hello           100ms
    timeout     idle            30s
    timeout     processing      15ms
    use-backend spoe-otel-backend

spoe-message on-http-request
    args unique-id=unique-id src=src method=method path=path host=req.hdr(Host) fe_name=fe_name fe_port=dst_port traceparent=req.hdr(traceparent)
    event on-frontend-http-request

spoe-message on-http-response
    args unique-id=unique-id status=status custom_attrs=var(txn.otel_attrs)
    event on-http-response
```

Key points:

- `var-prefix otel` means the agent's response variables are namespaced as `txn.otel.*`. The traceparent returned by the agent is available as `txn.otel.traceparent`.
- `set-on-error status` means HAProxy writes an error code to `txn.otel.status` if the agent is unreachable, so traffic is never blocked by a tracing failure.
- The `processing` timeout (15 ms) controls how long HAProxy waits for the agent before continuing. Keep this low to avoid adding latency to requests when the agent is slow.

### haproxy.cfg — frontend configuration

Every frontend that should emit traces needs three additions:

**1. Unique request ID** — used to correlate the request and response messages:

```haproxy
unique-id-format %[uuid()]
unique-id-header X-Request-ID
```

**2. SPOE filter** — activates the agent for this frontend:

```haproxy
filter spoe engine otel-agent config /etc/haproxy/spoe-otel.cfg
```

**3. Traceparent injection** — forwards the W3C `traceparent` header to the upstream so the trace can be continued there (only injected when the agent responded successfully):

```haproxy
http-request set-header traceparent %[var(txn.otel.traceparent)] \
  if { var(txn.otel.traceparent) -m found }
```

**Backend for the SPOE agent** — required once in `haproxy.cfg`, not per-frontend:

```haproxy
backend spoe-otel-backend
    mode    tcp
    balance roundrobin
    timeout connect 5ms
    timeout server  100ms
    server  otel-agent 127.0.0.1:12345
```

Adjust `server` to point to the address where the agent is listening (`SPOE_ADDR`). Multiple agent instances can be listed for load balancing.

### Full example

```haproxy
global
    log stdout format raw local0 info

defaults
    mode    http
    log     global
    option  httplog
    timeout connect 5s
    timeout client  30s
    timeout server  30s

frontend http_front
    bind *:80

    unique-id-format %[uuid()]
    unique-id-header X-Request-ID

    # Optional: per-frontend custom span attributes (see below)
    http-request set-var(txn.otel_attrs) str(environment=production;datacenter=eu-west-1)

    filter spoe engine otel-agent config /etc/haproxy/spoe-otel.cfg

    http-request set-header traceparent %[var(txn.otel.traceparent)] \
      if { var(txn.otel.traceparent) -m found }

    default_backend http_back

frontend api_front
    bind *:8443

    unique-id-format %[uuid()]
    unique-id-header X-Request-ID

    http-request set-var(txn.otel_attrs) str(environment=production;team=platform)

    filter spoe engine otel-agent config /etc/haproxy/spoe-otel.cfg

    http-request set-header traceparent %[var(txn.otel.traceparent)] \
      if { var(txn.otel.traceparent) -m found }

    default_backend api_back

backend http_back
    server app1 127.0.0.1:8080 check

backend api_back
    server api1 127.0.0.1:9090 check

backend spoe-otel-backend
    mode    tcp
    balance roundrobin
    timeout connect 5ms
    timeout server  100ms
    server  otel-agent 127.0.0.1:12345
```

---

## Trace context propagation

The agent uses the [W3C Trace Context](https://www.w3.org/TR/trace-context/) (`traceparent`) format.

**Inbound propagation:** If an incoming request carries a `traceparent` header, the agent extracts the trace and span IDs from it and starts the new span as a child of that remote parent. This allows an external caller's trace to flow through HAProxy and into the upstream service as a single connected trace.

**Outbound propagation:** After creating the span, the agent injects a new `traceparent` value (containing the current trace ID and the new span ID) into the `txn.otel.traceparent` transaction variable. The HAProxy `http-request set-header` rule shown above copies this value into the `traceparent` request header before the request is forwarded to the upstream. Any OpenTelemetry-instrumented upstream service will automatically pick this up and attach its own child spans to the same trace.

**If no traceparent is present on the inbound request**, the agent starts a new root span, generating a fresh trace ID.

---

## Per-frontend custom attributes

You can attach arbitrary string attributes to spans on a per-frontend basis using the `txn.otel_attrs` HAProxy variable. Set it to a semicolon-separated list of `key=value` pairs before the SPOE filter processes the request:

```haproxy
http-request set-var(txn.otel_attrs) str(environment=production;datacenter=eu-west-1;team=platform)
```

These attributes are sent to the agent in the `on-http-response` message and recorded on the span alongside the HTTP status code. This lets you tag spans with infrastructure context (environment, region, team, service tier, etc.) without touching the upstream application.

Rules:
- Pairs are separated by `;`
- Key and value are separated by `=`
- Whitespace around keys and values is trimmed
- Malformed pairs (missing `=`, empty key) are silently ignored
- Values are always recorded as strings

---

## Building from source

**Prerequisites:** Go 1.26 or later, `make`.

```bash
# Clone
git clone https://github.com/fgouteroux/haproxy-otel-spoe.git
cd haproxy-otel-spoe

# Build for the host platform
make build

# Cross-compile for linux/amd64 and linux/arm64 (output in dist/)
make build-all

# Run tests
make test

# Run tests with race detector
make test-race

# Tidy, format, vet, lint, then build
make all
```

Available `make` targets:

```
make help
```

### Version stamping

Binaries produced by `make build` or `make build-all` embed the Git tag, short commit hash, and build timestamp. These are visible in log output on startup and can be queried at runtime via the `Version`, `Commit`, and `BuildTime` variables in the `internal` package.

---

## Container image

No pre-built images are published. Build the image locally from the root `Containerfile`:

```bash
make docker-build
```

The image is built on `gcr.io/distroless/static-debian12:nonroot` — no shell, no package manager, runs as a non-root user. CA certificates are included for `OTLP_TLS=enabled`.

### Release verification

GitHub releases are signed with [cosign](https://docs.sigstore.dev/cosign/overview/) keyless signing. To verify the checksum file for a release:

```bash
cosign verify-blob \
  --certificate-identity "https://github.com/fgouteroux/haproxy-otel-spoe/.github/workflows/release.yml@refs/tags/<TAG>" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  --bundle checksums.txt.sigstore.json \
  checksums.txt
```

---

## Deployment notes

**Co-location vs. separate host.** The agent is stateful per-node: in-flight spans are stored in the process's memory, keyed by HAProxy's unique request ID. If you run multiple HAProxy nodes, each node must have its own agent instance. The agent does not share state across nodes.

**HAProxy backend timeouts.** The `spoe-otel-backend` timeouts (`connect 5ms`, `server 100ms`) are intentionally tight. The SPOE `processing` timeout in `spoe-otel.cfg` (15 ms) is the upper bound HAProxy will wait for the agent before proceeding. If the agent is overloaded or unreachable, HAProxy continues serving requests normally — tracing is best-effort.

**OTLP export reliability.** The exporter retries failed exports with exponential backoff (initial 5 s, max 30 s, up to 5 minutes total). Spans are buffered in memory (up to 10,000 spans) while retrying. Once the buffer is full, new spans are dropped and a warning is logged by the OTel SDK.

**Graceful shutdown.** On `SIGINT` or `SIGTERM`, the agent stops accepting new SPOE connections, ends all in-flight spans (recording them as incomplete), and flushes the exporter with a 10-second timeout before exiting.

**Sampling.** All spans are sampled (100%). If you need head-based sampling, configure it in your Tempo pipeline or a [Grafana Alloy](https://grafana.com/docs/alloy/) collector sitting in front of Tempo, rather than in the agent.

---

## License

See [LICENSE](LICENSE).
