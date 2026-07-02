// Command server runs the Service Constructor registry: a pure gRPC service
// backed by PostgreSQL. HTTP is served by the standalone gateway in front of it.
package main

import (
	"context"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"

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

	// Identity is verified centrally by the gateway and forwarded as gRPC
	// metadata (x-user-id / x-user-roles); this interceptor trusts it. engine is
	// reachable only in-cluster behind the gateway.
	interceptor := auth.UnaryServerInterceptor(auth.DefaultRoleResolver)

	grpcServer := grpc.NewServer(grpc.ChainUnaryInterceptor(interceptor))
	scv1.RegisterServiceRegistryServer(grpcServer, registrySrv)
	scv1.RegisterPaymentServiceServer(grpcServer, paymentSrv)

	// gRPC listener.
	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		return err
	}

	errCh := make(chan error, 2)
	go func() {
		log.Info("gRPC server listening", "addr", cfg.GRPCAddr)
		if err := grpcServer.Serve(lis); err != nil {
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
	_, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	grpcServer.GracefulStop()
	log.Info("shutdown complete")
	return nil
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
