package internal

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
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

// TLSMode controls how the gRPC connection to the OTLP backend is secured.
type TLSMode int

// TLS mode constants for the OTLP_TLS environment variable.
const (
	TLSDisabled           TLSMode = iota // plain TCP, no encryption (default for local/internal)
	TLSEnabled                           // verified TLS
	TLSInsecureSkipVerify                // TLS without certificate verification

	// TLSModeDisabled, TLSModeEnabled, TLSModeInsecureSkipVerify are the accepted
	// string values for the OTLP_TLS environment variable.
	TLSModeDisabled           = "disabled"
	TLSModeEnabled            = "enabled"
	TLSModeInsecureSkipVerify = "skip-verify"
)

// TLSConfig holds the TLS mode and optional paths to client cert/key (mTLS) and CA bundle.
type TLSConfig struct {
	Mode     TLSMode
	CertFile string // path to client certificate (mTLS)
	KeyFile  string // path to client private key (mTLS)
	CAFile   string // path to custom CA certificate (PEM)
}

// buildTLSConfig constructs a *tls.Config from the provided TLSConfig.
func buildTLSConfig(cfg TLSConfig) (*tls.Config, error) {
	tc := &tls.Config{}

	if cfg.Mode == TLSInsecureSkipVerify {
		tc.InsecureSkipVerify = true //nolint:gosec // intentional: skip-verify mode is opt-in for dev/testing
	}

	if cfg.CAFile != "" {
		pem, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read CA file %s: %w", cfg.CAFile, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("no valid certificates found in CA file %s", cfg.CAFile)
		}
		tc.RootCAs = pool
	}

	if cfg.CertFile != "" || cfg.KeyFile != "" {
		if cfg.CertFile == "" || cfg.KeyFile == "" {
			return nil, fmt.Errorf("OTLP_TLS_CERT and OTLP_TLS_KEY must both be set for mTLS")
		}
		cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("load client cert/key (%s, %s): %w", cfg.CertFile, cfg.KeyFile, err)
		}
		tc.Certificates = []tls.Certificate{cert}
	}

	return tc, nil
}

// SetupOTel initializes OpenTelemetry tracing with a gRPC OTLP exporter.
// It returns a shutdown function that must be called on exit to flush pending spans.
func SetupOTel(ctx context.Context, serviceName, endpoint string, tlsCfg TLSConfig) (func(context.Context) error, error) {
	var creds grpc.DialOption
	switch tlsCfg.Mode {
	case TLSEnabled, TLSInsecureSkipVerify:
		tc, err := buildTLSConfig(tlsCfg)
		if err != nil {
			return nil, err
		}
		creds = grpc.WithTransportCredentials(credentials.NewTLS(tc))
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
