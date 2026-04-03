# Local test environment

A Docker Compose stack for end-to-end testing of `haproxy-otel-spoe`.

## Services

| Service | Image / Source | Ports |
|---|---|---|
| `agent` | built from root `Containerfile` | internal only |
| `haproxy` | built from `test/Containerfile` | `8080` → frontend |
| `backend` | `traefik/whoami` | internal only |

Traffic flow:

```
curl → HAProxy :8080 → whoami backend
                 └── SPOE → agent → OTLP_ENDPOINT (gRPC OTLP)
```

## Requirements

- Docker + Docker Compose v2 (`docker compose`), **or** Podman + `podman-compose`

## Configuration

Edit `docker-compose.yml` to set the OTLP endpoint for the agent:

```yaml
agent:
  environment:
    OTLP_ENDPOINT: tempo.internal:4317   # your Tempo / OTLP collector
    OTLP_TLS: disabled                   # or: enabled, skip-verify
```

To run a local Tempo instead, uncomment the `tempo` service block in `docker-compose.yml`.

## Start the stack

```bash
cd test/
docker compose up --build -d
docker compose ps
```

## Send test requests

```bash
# Basic GET
curl -i http://localhost:8080/

# POST with a body
curl -i -X POST http://localhost:8080/api -d '{"hello":"world"}'

# Simulate an inbound distributed trace (continues the upstream trace)
curl -i -H 'traceparent: 00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01' \
     http://localhost:8080/

# Send several requests to generate data
for i in $(seq 1 10); do curl -s http://localhost:8080/ > /dev/null; done
```

## Query traces with curl (local Tempo)

If you are running the local Tempo service, all queries go to `http://localhost:3200`.

### Search recent traces

```bash
curl -s "http://localhost:3200/api/search" | jq .
```

Search within a time window (Unix timestamps):

```bash
START=$(date -d '30 minutes ago' +%s 2>/dev/null || date -v-30M +%s)
END=$(date +%s)
curl -s "http://localhost:3200/api/search?start=${START}&end=${END}" | jq .
```

### Search by tag (key=value)

```bash
# By HTTP method
curl -s "http://localhost:3200/api/search?tags=http.method%3DGET" | jq .

# By service name
curl -s "http://localhost:3200/api/search?tags=service.name%3Dhaproxy" | jq .

# By custom attribute set in haproxy.cfg (environment=test)
curl -s "http://localhost:3200/api/search?tags=environment%3Dtest" | jq .

# Multiple tags (space-separated, URL-encoded as %20)
curl -s "http://localhost:3200/api/search?tags=service.name%3Dhaproxy%20environment%3Dtest" | jq .
```

### TraceQL queries

```bash
# All spans from the haproxy service
curl -s --get "http://localhost:3200/api/search" \
     --data-urlencode 'q={resource.service.name="haproxy"}' | jq .

# HTTP 5xx errors
curl -s --get "http://localhost:3200/api/search" \
     --data-urlencode 'q={http.status_code>=500}' | jq .

# Requests to a specific path
curl -s --get "http://localhost:3200/api/search" \
     --data-urlencode 'q={http.target="/api"}' | jq .
```

### Fetch a specific trace by ID

```bash
TRACE_ID=4bf92f3577b34da6a3ce929d0e0e4736
curl -s "http://localhost:3200/api/traces/${TRACE_ID}" | jq .
```

### Pretty-print span attributes

```bash
curl -s "http://localhost:3200/api/search" | \
  jq '.traces[] | {traceID, rootName, rootServiceName, durationMs}'
```

## Stop the stack

```bash
docker compose down
```
