// Package config loads runtime configuration from the environment.
package config

import (
	"fmt"
	"os"
	"time"
)

// Config holds all runtime settings.
type Config struct {
	// DatabaseURL is the Postgres DSN, e.g.
	// postgres://sc:sc@localhost:5432/service_constructor?sslmode=disable
	DatabaseURL string
	// GRPCAddr is the listen address for the gRPC server.
	GRPCAddr string
	// ShutdownTimeout bounds graceful shutdown.
	ShutdownTimeout time.Duration

	// DeviceKeyPEM is a static device public key (PEM) used by the local
	// StaticDeviceKeyResolver to verify consent signatures. In production a real
	// DeviceKeyResolver replaces this.
	DeviceKeyPEM string

	// ExecutorMode selects the saga executor: "http" calls each service's real
	// executeUrl; "mock" (default for dev) returns a canned result.
	ExecutorMode string
	// ExecuteTimeout bounds a single execute HTTP call.
	ExecuteTimeout time.Duration

	// ConsentMode gates device-signed consent on /pay: "device" (default) requires
	// a device-signed consent; "none" trusts the authenticated session, which is
	// how a trusted shell (the personal cabinet) drives payment.
	ConsentMode string

	// LedgerMode selects the settlement backend: "grpc" calls the real ledger
	// service; "mock" (default for dev) uses an in-memory ledger.
	LedgerMode string
	// LedgerAddr is the ledger gRPC endpoint (host:port) when LedgerMode=grpc.
	LedgerAddr string
}

// Load reads configuration from the environment, applying defaults.
func Load() (Config, error) {
	c := Config{
		DatabaseURL:     env("DATABASE_URL", "postgres://sc:sc@localhost:5432/service_constructor?sslmode=disable"),
		GRPCAddr:        env("GRPC_ADDR", ":9090"),
		ShutdownTimeout: 10 * time.Second,
		DeviceKeyPEM:    os.Getenv("DEVICE_KEY_PEM"),
		ExecutorMode:    env("EXECUTOR_MODE", "mock"),
		ExecuteTimeout:  10 * time.Second,
		ConsentMode:     env("CONSENT_MODE", "device"),
		LedgerMode:      env("LEDGER_MODE", "mock"),
		LedgerAddr:      env("LEDGER_ADDR", "localhost:9110"),
	}
	if c.DatabaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}
	return c, nil
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
