package internal

import (
	"context"
	"crypto/tls"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// TLSMode controls how the gRPC connection to Tempo is secured.
type TLSMode int

const (
	TLSDisabled           TLSMode = iota // plain TCP, no encryption (default for local/internal)
	TLSEnabled                           // verified TLS
	TLSInsecureSkipVerify                // TLS without certificate verification

	// TLSModeDisabled, TLSModeEnabled, TLSModeInsecureSkipVerify are the accepted
	// values for the OTLP_TLS environment variable.
	TLSModeDisabled           = "disabled"
	TLSModeEnabled            = "enabled"
	TLSModeInsecureSkipVerify = "skip-verify"
)

func SetupOTel(ctx context.Context, serviceName, endpoint string, tlsMode TLSMode) (func(context.Context) error, error) {
	var creds grpc.DialOption
	switch tlsMode {
	case TLSEnabled:
		creds = grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{}))
	case TLSInsecureSkipVerify:
		creds = grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{InsecureSkipVerify: true})) //nolint:gosec
	default:
		creds = grpc.WithTransportCredentials(insecure.NewCredentials())
	}

	//nolint:staticcheck // grpc.Dial is deprecated in v1.63+ but we stay on v1.59
	conn, err := grpc.Dial(endpoint, creds)
	if err != nil {
		return nil, fmt.Errorf("failed to create gRPC client for %s: %w", endpoint, err)
	}

	exporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithGRPCConn(conn),
		otlptracegrpc.WithRetry(otlptracegrpc.RetryConfig{
			Enabled:         true,
			InitialInterval: 5 * time.Second,
			MaxInterval:     30 * time.Second,
			MaxElapsedTime:  5 * time.Minute,
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create OTLP exporter: %w", err)
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceNameKey.String(serviceName),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create OTel resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter,
			// Buffer up to 10k spans in memory while Tempo is unavailable.
			// When full, new spans are dropped (logged as warnings by the SDK).
			sdktrace.WithMaxQueueSize(10000),
			sdktrace.WithMaxExportBatchSize(512),
			sdktrace.WithBatchTimeout(5*time.Second),
		),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	return func(ctx context.Context) error {
		err := tp.Shutdown(ctx)
		conn.Close() //nolint:errcheck // best-effort on shutdown
		return err
	}, nil
}
