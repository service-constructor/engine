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
	// HTTPAddr is the listen address for the HTTP gateway.
	HTTPAddr string
	// ShutdownTimeout bounds graceful shutdown.
	ShutdownTimeout time.Duration

	// AuthMode selects the built-in authenticator: "jwt" (default) or "none"
	// (dev only — accepts every request). Integrators replacing auth entirely
	// supply their own Authenticator in code and can ignore this.
	AuthMode string
	// AuthJWTSecret is the HMAC secret for the built-in JWT authenticator.
	AuthJWTSecret string

	// DeviceKeyPEM is a static device public key (PEM) used by the local
	// StaticDeviceKeyResolver to verify consent signatures. In production a real
	// DeviceKeyResolver replaces this.
	DeviceKeyPEM string

	// ExecutorMode selects the saga executor: "http" calls each service's real
	// executeUrl; "mock" (default for dev) returns a canned result.
	ExecutorMode string
	// ExecuteTimeout bounds a single execute HTTP call.
	ExecuteTimeout time.Duration
}

// Load reads configuration from the environment, applying defaults.
func Load() (Config, error) {
	c := Config{
		DatabaseURL:     env("DATABASE_URL", "postgres://sc:sc@localhost:5432/service_constructor?sslmode=disable"),
		GRPCAddr:        env("GRPC_ADDR", ":9090"),
		HTTPAddr:        env("HTTP_ADDR", ":8080"),
		ShutdownTimeout: 10 * time.Second,
		AuthMode:        env("AUTH_MODE", "jwt"),
		AuthJWTSecret:   os.Getenv("AUTH_JWT_SECRET"),
		DeviceKeyPEM:    os.Getenv("DEVICE_KEY_PEM"),
		ExecutorMode:    env("EXECUTOR_MODE", "mock"),
		ExecuteTimeout:  10 * time.Second,
	}
	if c.AuthMode == "jwt" && c.AuthJWTSecret == "" {
		return Config{}, fmt.Errorf("AUTH_JWT_SECRET is required when AUTH_MODE=jwt (set AUTH_MODE=none for dev)")
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
