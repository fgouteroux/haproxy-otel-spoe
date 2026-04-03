# ---------------------------------------------------------------------------
# Stage 1: Build
# ---------------------------------------------------------------------------
FROM golang:1.26.1-alpine AS builder

WORKDIR /app

# Download dependencies first (layer-cached separately from source)
COPY go.mod go.sum ./
RUN go mod download && go mod verify

COPY . .

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_TIME=unknown

RUN CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags "-s -w \
        -X github.com/fgouteroux/haproxy-otel-spoe/internal.Version=${VERSION} \
        -X github.com/fgouteroux/haproxy-otel-spoe/internal.Commit=${COMMIT} \
        -X github.com/fgouteroux/haproxy-otel-spoe/internal.BuildTime=${BUILD_TIME}" \
      -o haproxy-otel-spoe .

# ---------------------------------------------------------------------------
# Stage 2: Runtime — distroless/static includes CA certs, no shell, non-root
# ---------------------------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /app/haproxy-otel-spoe /usr/local/bin/haproxy-otel-spoe

EXPOSE 12345

ENTRYPOINT ["/usr/local/bin/haproxy-otel-spoe"]
