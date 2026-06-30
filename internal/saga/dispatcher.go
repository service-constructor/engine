package saga

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/nvsces/service-constructor/internal/domain"
)

// Dispatcher applies outbox entries to the Ledger (white paper section 11). The
// orchestrator records capture/release entries transactionally with the order
// transition; this background process reads undispatched entries, applies the
// real ledger operation idempotently (ledger ops are idempotent by orderId),
// and marks them dispatched. A crash between apply and mark just re-applies the
// (idempotent) op next pass — at-least-once delivery with idempotent effects.
type Dispatcher struct {
	store    OutboxStore
	ledger   Ledger
	log      *slog.Logger
	interval time.Duration
	batch    int
}

// DispatcherOption configures a Dispatcher.
type DispatcherOption func(*Dispatcher)

func WithDispatchInterval(d time.Duration) DispatcherOption {
	return func(p *Dispatcher) { p.interval = d }
}
func WithDispatchBatch(n int) DispatcherOption {
	return func(p *Dispatcher) { p.batch = n }
}

// NewDispatcher builds an outbox dispatcher.
func NewDispatcher(store OutboxStore, ledger Ledger, log *slog.Logger, opts ...DispatcherOption) *Dispatcher {
	d := &Dispatcher{
		store:    store,
		ledger:   ledger,
		log:      log,
		interval: 1 * time.Second,
		batch:    100,
	}
	for _, o := range opts {
		o(d)
	}
	return d
}

// Run dispatches on a ticker until ctx is cancelled.
func (d *Dispatcher) Run(ctx context.Context) {
	ticker := time.NewTicker(d.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if n, err := d.DispatchOnce(ctx); err != nil {
				d.log.Warn("outbox dispatch failed", "err", err)
			} else if n > 0 {
				d.log.Info("outbox entries dispatched", "count", n)
			}
		}
	}
}

// DispatchOnce applies all currently-pending entries and returns how many were
// dispatched. An entry whose apply fails is left undispatched for the next pass.
func (d *Dispatcher) DispatchOnce(ctx context.Context) (int, error) {
	entries, err := d.store.ListUndispatched(ctx, d.batch)
	if err != nil {
		return 0, err
	}
	done := 0
	for _, e := range entries {
		if err := d.apply(ctx, e); err != nil {
			d.log.Warn("apply outbox entry failed; will retry", "id", e.ID, "op", e.Op, "order", e.OrderID, "err", err)
			continue
		}
		if err := d.store.MarkDispatched(ctx, e.ID); err != nil {
			// Apply succeeded but mark failed: next pass re-applies idempotently.
			d.log.Warn("mark dispatched failed; will re-apply (idempotent)", "id", e.ID, "err", err)
			continue
		}
		done++
	}
	return done, nil
}

// apply runs the ledger operation for one entry. Ledger ops are idempotent by
// orderId, so re-applying after a crash is safe.
func (d *Dispatcher) apply(ctx context.Context, e *domain.OutboxEntry) error {
	switch e.Op {
	case domain.OutboxCapture:
		return d.ledger.Capture(ctx, CaptureRequest{
			OrderID:           e.OrderID,
			Net:               str(e.Payload["net"]),
			Fee:               str(e.Payload["fee"]),
			ReceivingWalletID: str(e.Payload["receivingWalletId"]),
			CurrencyID:        int64FromAny(e.Payload["currencyId"]),
		})
	case domain.OutboxRelease:
		return d.ledger.Release(ctx, e.OrderID)
	default:
		return fmt.Errorf("unknown outbox op %q", e.Op)
	}
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

// int64FromAny coerces a JSON number (float64) or int back to int64.
func int64FromAny(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	default:
		return 0
	}
}
