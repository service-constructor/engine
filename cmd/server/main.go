// Command server runs the Service Constructor registry: a gRPC service with an
// HTTP/JSON gateway in front of it, backed by PostgreSQL.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	scv1 "github.com/service-constructor/engine/gen/serviceconstructor/v1"
	"github.com/service-constructor/engine/internal/auth"
	"github.com/service-constructor/engine/internal/config"
	"github.com/service-constructor/engine/internal/domain"
	"github.com/service-constructor/engine/internal/ledgerclient"
	"github.com/service-constructor/engine/internal/repository/postgres"
	"github.com/service-constructor/engine/internal/saga"
	"github.com/service-constructor/engine/internal/server"
	"github.com/service-constructor/engine/internal/service"
)

// registryLookup adapts the service Registry to saga.ServiceLookup. Payment is
// system-initiated, so the lookup is unscoped (ScopeAll).
type registryLookup struct {
	reg *service.Registry
}

func (l registryLookup) Lookup(ctx context.Context, serviceID string) (*domain.Service, error) {
	return l.reg.Get(ctx, service.ScopeAll, serviceID)
}

// ListActive returns all ACTIVE services (unscoped) for the public catalog the
// shell renders. Paginates through the registry to gather every page.
func (l registryLookup) ListActive(ctx context.Context) ([]*domain.Service, error) {
	var all []*domain.Service
	token := ""
	for {
		page, next, err := l.reg.List(ctx, service.ScopeAll, service.ListFilter{
			Status:    domain.StatusActive,
			PageSize:  100,
			PageToken: token,
		})
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		if next == "" {
			break
		}
		token = next
	}
	return all, nil
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("server exited with error", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Apply DB migrations before opening the application pool.
	log.Info("applying migrations")
	if err := postgres.Migrate(cfg.DatabaseURL); err != nil {
		return err
	}

	pool, err := postgres.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	repo := postgres.NewServiceRepository(pool)
	reg := service.NewRegistry(repo)
	registrySrv := server.NewRegistryServer(reg)

	// Payment saga: orchestrator over the order store. The Ledger is a mock for
	// local runs; the Executor is selected by EXECUTOR_MODE. The same ledger is
	// shared with the outbox dispatcher (freeze is synchronous; capture/release
	// are applied from the outbox).
	orderRepo := postgres.NewOrderRepository(pool)
	executor := buildExecutor(cfg, log)
	ledger, closeLedger, err := buildLedger(cfg, log)
	if err != nil {
		return err
	}
	defer closeLedger()
	orch := saga.New(
		registryLookup{reg},
		orderRepo,
		ledger,
		executor,
		saga.StaticDeviceKeyResolver{PEM: cfg.DeviceKeyPEM},
		saga.WithRequireConsent(cfg.ConsentMode != "none"),
	)
	if cfg.ConsentMode == "none" {
		log.Warn("CONSENT_MODE=none: /pay trusts the authenticated session; no device-signed consent required")
	}
	paymentSrv := server.NewPaymentServer(orch, orderRepo, registryLookup{reg})

	// Reconciler: background process that finalizes stuck orders, querying the
	// service statusUrl before any compensation (query-before-compensate).
	statusChecker := saga.NewHTTPStatusChecker(5 * time.Second)
	reconciler := saga.NewReconciler(orch, orderRepo, statusChecker, log)

	// Outbox dispatcher: applies capture/release entries to the ledger,
	// idempotently, decoupled from the order transition that recorded them.
	dispatcher := saga.NewDispatcher(orderRepo, ledger, log)

	// Authentication is pluggable: an integrator can replace buildAuthenticator
	// with their own Authenticator without touching the registry or transport.
	authn, err := buildAuthenticator(cfg, log)
	if err != nil {
		return err
	}
	interceptor := auth.UnaryServerInterceptor(authn, auth.DefaultRoleResolver)

	grpcServer := grpc.NewServer(grpc.ChainUnaryInterceptor(interceptor))
	scv1.RegisterServiceRegistryServer(grpcServer, registrySrv)
	scv1.RegisterPaymentServiceServer(grpcServer, paymentSrv)

	// gRPC listener.
	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		return err
	}

	// HTTP gateway dials the gRPC server over loopback and proxies REST → gRPC.
	gwMux := runtime.NewServeMux()
	dialOpts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
	if err := scv1.RegisterServiceRegistryHandlerFromEndpoint(ctx, gwMux, cfg.GRPCAddr, dialOpts); err != nil {
		return err
	}
	if err := scv1.RegisterPaymentServiceHandlerFromEndpoint(ctx, gwMux, cfg.GRPCAddr, dialOpts); err != nil {
		return err
	}
	httpServer := &http.Server{Addr: cfg.HTTPAddr, Handler: gwMux}

	errCh := make(chan error, 2)
	go func() {
		log.Info("gRPC server listening", "addr", cfg.GRPCAddr)
		if err := grpcServer.Serve(lis); err != nil {
			errCh <- err
		}
	}()
	go func() {
		log.Info("HTTP gateway listening", "addr", cfg.HTTPAddr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()
	go func() {
		log.Info("reconciler started", "interval", "30s")
		reconciler.Run(ctx)
	}()
	go func() {
		log.Info("outbox dispatcher started", "interval", "1s")
		dispatcher.Run(ctx)
	}()

	select {
	case <-ctx.Done():
		log.Info("shutdown signal received")
	case err := <-errCh:
		return err
	}

	// Graceful shutdown.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Warn("http shutdown", "err", err)
	}
	grpcServer.GracefulStop()
	log.Info("shutdown complete")
	return nil
}

// buildAuthenticator selects the built-in Authenticator from config. Integrators
// adopting this module can replace this function with one that returns their own
// auth.Authenticator implementation.
func buildAuthenticator(cfg config.Config, log *slog.Logger) (auth.Authenticator, error) {
	switch cfg.AuthMode {
	case "none":
		log.Warn("AUTH_MODE=none: admin API is UNAUTHENTICATED — do not use in production")
		return auth.AllowAll{}, nil
	case "jwt", "":
		log.Info("using JWT authenticator")
		return auth.NewJWTAuthenticator([]byte(cfg.AuthJWTSecret)), nil
	default:
		return nil, fmt.Errorf("unknown AUTH_MODE %q", cfg.AuthMode)
	}
}

// buildExecutor selects the saga executor from config. EXECUTOR_MODE=http calls
// each service's real executeUrl (with retries + circuit breaker); the default
// "mock" returns a canned result for local runs. MOCK_EXECUTE_STATUS overrides
// the mock verdict to exercise saga branches.
func buildExecutor(cfg config.Config, log *slog.Logger) saga.Executor {
	if cfg.ExecutorMode == "http" {
		log.Info("using HTTP executor", "timeout", cfg.ExecuteTimeout)
		return saga.NewHTTPExecutor(cfg.ExecuteTimeout, 5, 30*time.Second)
	}
	log.Info("using mock executor", "mode", cfg.ExecutorMode)
	mockExec := saga.NewMockExecutor()
	if s := os.Getenv("MOCK_EXECUTE_STATUS"); s != "" {
		mockExec.Result = saga.ExecuteResult{Status: saga.ExecuteStatus(s), ExternalRef: "mock-ref"}
	}
	return mockExec
}

// buildLedger selects the settlement backend. LEDGER_MODE=grpc settles against
// the real ledger service; "mock" (default) uses an in-memory ledger. It returns
// a cleanup func (a no-op for the mock) the caller defers.
func buildLedger(cfg config.Config, log *slog.Logger) (saga.Ledger, func(), error) {
	if cfg.LedgerMode == "grpc" {
		log.Info("using gRPC ledger", "addr", cfg.LedgerAddr)
		c, err := ledgerclient.Dial(cfg.LedgerAddr)
		if err != nil {
			return nil, nil, err
		}
		return c, func() { _ = c.Close() }, nil
	}
	log.Info("using mock ledger", "mode", cfg.LedgerMode)
	return saga.NewMockLedger(), func() {}, nil
}
