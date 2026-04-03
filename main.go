package main

import (
	"context"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/negasus/haproxy-spoe-go/agent"
	"github.com/negasus/haproxy-spoe-go/logger"

	"github.com/fgouteroux/haproxy-otel-spoe/internal"
)

func main() {
	otlpEndpoint := getEnv("OTLP_ENDPOINT", "localhost:4317")
	serviceName := getEnv("SERVICE_NAME", "haproxy")
	spoeAddr := getEnv("SPOE_ADDR", "0.0.0.0:12345")
	tlsMode := internal.TLSDisabled
	switch getEnv("OTLP_TLS", internal.TLSModeDisabled) {
	case internal.TLSModeEnabled:
		tlsMode = internal.TLSEnabled
	case internal.TLSModeInsecureSkipVerify:
		tlsMode = internal.TLSInsecureSkipVerify
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	shutdown, err := internal.SetupOTel(ctx, serviceName, otlpEndpoint, tlsMode)
	if err != nil {
		log.Printf("failed to setup OTel: %v", err)
		cancel()
		return
	}

	store := internal.NewStore(30 * time.Second)
	h := internal.NewHandler(store)

	lc := net.ListenConfig{}
	listener, err := lc.Listen(ctx, "tcp4", spoeAddr)
	if err != nil {
		log.Printf("failed to listen on %s: %v", spoeAddr, err)
		cancel()
		return
	}
	defer func() {
		if err := listener.Close(); err != nil {
			log.Printf("failed to close listener: %v", err)
		}
	}()

	log.Printf("SPOE agent listening on %s", spoeAddr)
	log.Printf("Sending traces to OTLP endpoint %s", otlpEndpoint)

	go func() {
		a := agent.New(h.Handle, logger.NewDefaultLog())
		if err := a.Serve(listener); err != nil {
			log.Printf("SPOE agent stopped: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("Shutting down, flushing spans...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	store.Close()

	if err := shutdown(shutdownCtx); err != nil {
		log.Printf("OTel shutdown error: %v", err)
	}
	log.Println("Shutdown complete")
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
