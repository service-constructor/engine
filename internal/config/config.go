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
}

// Load reads configuration from the environment, applying defaults.
func Load() (Config, error) {
	c := Config{
		DatabaseURL:     env("DATABASE_URL", "postgres://sc:sc@localhost:5432/service_constructor?sslmode=disable"),
		GRPCAddr:        env("GRPC_ADDR", ":9090"),
		HTTPAddr:        env("HTTP_ADDR", ":8080"),
		ShutdownTimeout: 10 * time.Second,
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
