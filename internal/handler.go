package internal

import (
	"context"
	"fmt"
	"log"
	"net"
	"runtime/debug"
	"strings"

	"github.com/negasus/haproxy-spoe-go/action"
	"github.com/negasus/haproxy-spoe-go/request"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	"go.opentelemetry.io/otel/trace"
)

// Handler processes SPOE messages from HAProxy and manages the span lifecycle.
type Handler struct {
	store      *Store
	tracer     trace.Tracer
	propagator propagation.TextMapPropagator
}

// NewHandler creates a new Handler backed by the given span store.
func NewHandler(store *Store) *Handler {
	return &Handler{
		store:      store,
		tracer:     otel.Tracer("haproxy"),
		propagator: otel.GetTextMapPropagator(),
	}
}

// Handle processes an incoming SPOE message — either on-http-request (opens a span),
// on-backend-http-request (opens a child span for the backend leg),
// or on-http-response (closes both spans with status and custom attributes).
func (h *Handler) Handle(req *request.Request) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic in SPOE handler: %v\n%s", r, debug.Stack())
		}
	}()

	if msg, err := req.Messages.GetByName("on-http-request"); err == nil {
		get := msg.KV.Get
		uniqueID := kvString(get, "unique-id")
		method := kvString(get, "method")
		path := kvString(get, "path")
		host := kvString(get, "host")
		srcIP := kvIP(get, "src")
		feName := kvString(get, "fe_name")
		fePort := kvInt(get, "fe_port")
		traceparentHeader := kvString(get, "traceparent")

		// Extract upstream trace context if present, so HAProxy continues
		// an existing trace rather than always starting a root span.
		carrier := propagation.MapCarrier{"traceparent": traceparentHeader}
		ctx := h.propagator.Extract(context.Background(), carrier)

		ctx, span := h.tracer.Start(ctx,
			method+" "+path,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				semconv.HTTPMethodKey.String(method),
				semconv.HTTPTargetKey.String(path),
				semconv.ServerAddressKey.String(host),
				semconv.ClientAddressKey.String(srcIP),
				attribute.String("haproxy.frontend.name", feName),
				attribute.Int("haproxy.frontend.port", fePort),
			),
		)

		h.store.Set(uniqueID, span)

		// Inject via the propagator so TraceFlags are respected and the format
		// stays correct if the propagator is ever swapped (e.g. B3).
		outCarrier := propagation.MapCarrier{}
		h.propagator.Inject(ctx, outCarrier)
		req.Actions.SetVar(action.ScopeTransaction, "traceparent", outCarrier["traceparent"])
		return
	}

	if msg, err := req.Messages.GetByName("on-backend-http-request"); err == nil {
		get := msg.KV.Get
		uniqueID := kvString(get, "unique-id")
		method := kvString(get, "method")
		path := kvString(get, "path")

		parentSpan, ok := h.store.Get(uniqueID)
		if !ok {
			// Parent span evicted or unknown request — skip.
			return
		}

		// Start a client-kind child span representing the backend call.
		// be_name and srv_name are not available at this HAProxy event stage;
		// they are added as attributes when the span is closed in on-http-response.
		ctx := trace.ContextWithSpan(context.Background(), parentSpan)
		_, backendSpan := h.tracer.Start(ctx,
			method+" "+path,
			trace.WithSpanKind(trace.SpanKindClient),
			trace.WithAttributes(
				semconv.HTTPMethodKey.String(method),
				semconv.HTTPTargetKey.String(path),
			),
		)
		h.store.Set(uniqueID+":backend", backendSpan)
		return
	}

	if msg, err := req.Messages.GetByName("on-http-response"); err == nil {
		get := msg.KV.Get
		uniqueID := kvString(get, "unique-id")
		statusCode := kvInt(get, "status")
		beName := kvString(get, "be_name")
		srvName := kvString(get, "srv_name")
		customAttrs := kvString(get, "custom_attrs")

		span, ok := h.store.Get(uniqueID)
		if !ok {
			// Span already evicted by TTL cleanup or unknown request.
			return
		}
		defer h.store.Delete(uniqueID)

		// End backend child span first so it is nested inside the frontend span.
		// be_name and srv_name are only available at response time in HAProxy.
		if backendSpan, ok := h.store.Get(uniqueID + ":backend"); ok {
			backendSpan.SetAttributes(
				semconv.HTTPStatusCodeKey.Int(statusCode),
				attribute.String("haproxy.backend.name", beName),
				attribute.String("haproxy.server.name", srvName),
			)
			if statusCode >= 500 {
				backendSpan.SetStatus(codes.Error, fmt.Sprintf("HTTP %d", statusCode))
			} else {
				backendSpan.SetStatus(codes.Ok, "")
			}
			backendSpan.End()
			h.store.Delete(uniqueID + ":backend")
		}

		span.SetAttributes(append(
			[]attribute.KeyValue{semconv.HTTPStatusCodeKey.Int(statusCode)},
			parseCustomAttrs(customAttrs)...,
		)...)

		if statusCode >= 500 {
			span.SetStatus(codes.Error, fmt.Sprintf("HTTP %d", statusCode))
		} else {
			span.SetStatus(codes.Ok, "")
		}

		span.End()
		return
	}
}

// parseCustomAttrs parses a semicolon-separated "key=value;key=value" string
// into OTel attributes. Malformed pairs are silently skipped.
func parseCustomAttrs(raw string) []attribute.KeyValue {
	if raw == "" {
		return nil
	}
	var attrs []attribute.KeyValue
	for raw != "" {
		var pair string
		if i := strings.IndexByte(raw, ';'); i >= 0 {
			pair, raw = raw[:i], raw[i+1:]
		} else {
			pair, raw = raw, ""
		}
		k, v, ok := strings.Cut(pair, "=")
		if !ok || k == "" {
			continue
		}
		attrs = append(attrs, attribute.String(strings.TrimSpace(k), strings.TrimSpace(v)))
	}
	return attrs
}

func kvString(get func(string) (any, bool), key string) string {
	v, ok := get(key)
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

func kvIP(get func(string) (any, bool), key string) string {
	v, ok := get(key)
	if !ok {
		return ""
	}
	ip, _ := v.(net.IP)
	if ip == nil {
		return ""
	}
	return ip.String()
}

func kvInt(get func(string) (any, bool), key string) int {
	v, ok := get(key)
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case int:
		return n
	case int32:
		return int(n)
	case int64:
		return int(n)
	case uint32:
		return int(n)
	}
	return 0
}
