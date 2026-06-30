package saga

import (
	"context"
	"log/slog"
	"time"
)

// Reconciler is the background process that drives stuck orders to a final
// state (white paper section 11). It scans for orders in intermediate states
// past their freeze TTL and reconciles each via the orchestrator, applying
// query-before-compensate so funds are never released blindly.
type Reconciler struct {
	orch   *Orchestrator
	orders OrderStore
	status StatusChecker
	log    *slog.Logger
	// interval between scans.
	interval time.Duration
	// batch is the maximum orders processed per scan.
	batch int
	// now provides the current time (overridable in tests).
	now func() time.Time
}

// ReconcilerOption configures a Reconciler.
type ReconcilerOption func(*Reconciler)

func WithInterval(d time.Duration) ReconcilerOption {
	return func(r *Reconciler) { r.interval = d }
}
func WithBatch(n int) ReconcilerOption { return func(r *Reconciler) { r.batch = n } }
func WithReconcilerClock(now func() time.Time) ReconcilerOption {
	return func(r *Reconciler) { r.now = now }
}

// NewReconciler builds a Reconciler.
func NewReconciler(orch *Orchestrator, orders OrderStore, status StatusChecker, log *slog.Logger, opts ...ReconcilerOption) *Reconciler {
	r := &Reconciler{
		orch:     orch,
		orders:   orders,
		status:   status,
		log:      log,
		interval: 30 * time.Second,
		batch:    100,
		now:      time.Now,
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

// Run scans on a ticker until ctx is cancelled. Intended to run in a goroutine.
func (r *Reconciler) Run(ctx context.Context) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if n, err := r.ReconcileOnce(ctx); err != nil {
				r.log.Warn("reconcile pass failed", "err", err)
			} else if n > 0 {
				r.log.Info("reconciled stuck orders", "count", n)
			}
		}
	}
}

// ReconcileOnce performs a single scan-and-reconcile pass and returns the number
// of orders whose state advanced.
func (r *Reconciler) ReconcileOnce(ctx context.Context) (int, error) {
	stuck, err := r.orders.ListStuck(ctx, r.now().UTC(), r.batch)
	if err != nil {
		return 0, err
	}
	advanced := 0
	for _, o := range stuck {
		before := o.State
		res, err := r.orch.ReconcileOrder(ctx, r.status, o)
		if err != nil {
			r.log.Warn("reconcile order failed", "order", o.ID, "state", o.State, "err", err)
			continue
		}
		if res.State != before {
			advanced++
			r.log.Info("order reconciled", "order", o.ID, "from", before, "to", res.State)
		}
	}
	return advanced, nil
}
