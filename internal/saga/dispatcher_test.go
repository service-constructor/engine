package saga

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"

	"github.com/nvsces/service-constructor/internal/domain"
)

func quietLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(discard{}, nil))
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

// flakyLedger fails Capture the first failTimes calls, then succeeds.
type flakyLedger struct {
	*MockLedger
	failTimes int32
	calls     int32
}

func (f *flakyLedger) Capture(ctx context.Context, req CaptureRequest) error {
	if atomic.AddInt32(&f.calls, 1) <= atomic.LoadInt32(&f.failTimes) {
		return errors.New("ledger temporarily unavailable")
	}
	return f.MockLedger.Capture(ctx, req)
}

func seedCaptureEntry(store *MemOrderStore, orderID string) {
	order := &domain.Order{ID: orderID, ServiceID: "s", QuoteNonce: "n-" + orderID, State: domain.OrderExecuted}
	_ = store.Create(context.Background(), order, &domain.OrderTransition{OrderID: orderID, ToState: domain.OrderExecuted, Reason: "seed"})
	order.State = domain.OrderCompleted
	_ = store.SaveWithOutbox(context.Background(), order,
		&domain.OrderTransition{OrderID: orderID, FromState: domain.OrderExecuted, ToState: domain.OrderCompleted, Reason: "seed"},
		&domain.OutboxEntry{
			OrderID: orderID,
			Op:      domain.OutboxCapture,
			Payload: map[string]any{"net": "4.75", "fee": "0.25", "receivingWalletId": "wlt_r", "currencyId": int64(1)},
		})
}

func TestDispatcherAppliesCapture(t *testing.T) {
	store := NewMemOrderStore()
	ledger := NewMockLedger()
	seedCaptureEntry(store, "ord_1")

	d := NewDispatcher(store, ledger, quietLog())
	n, err := d.DispatchOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("dispatched = %d, want 1", n)
	}
	cap, ok := ledger.Captured("ord_1")
	if !ok {
		t.Fatal("expected capture applied")
	}
	if cap.Net != "4.75" || cap.Fee != "0.25" || cap.ReceivingWalletID != "wlt_r" || cap.CurrencyID != 1 {
		t.Fatalf("capture payload wrong: %+v", cap)
	}

	// Second pass: entry already dispatched, nothing to do.
	n2, _ := d.DispatchOnce(context.Background())
	if n2 != 0 {
		t.Fatalf("second pass dispatched = %d, want 0 (idempotent)", n2)
	}
}

func TestDispatcherRetriesOnLedgerError(t *testing.T) {
	store := NewMemOrderStore()
	ledger := &flakyLedger{MockLedger: NewMockLedger(), failTimes: 2}
	seedCaptureEntry(store, "ord_retry")

	d := NewDispatcher(store, ledger, quietLog())
	ctx := context.Background()

	// First two passes fail to apply; the entry stays undispatched.
	if n, _ := d.DispatchOnce(ctx); n != 0 {
		t.Fatalf("pass1 dispatched = %d, want 0", n)
	}
	if n, _ := d.DispatchOnce(ctx); n != 0 {
		t.Fatalf("pass2 dispatched = %d, want 0", n)
	}
	if _, ok := ledger.Captured("ord_retry"); ok {
		t.Fatal("capture should not have succeeded yet")
	}
	// Third pass succeeds.
	if n, _ := d.DispatchOnce(ctx); n != 1 {
		t.Fatalf("pass3 dispatched = %d, want 1", n)
	}
	if _, ok := ledger.Captured("ord_retry"); !ok {
		t.Fatal("expected capture after retries")
	}
}

func TestDispatcherReleaseOp(t *testing.T) {
	store := NewMemOrderStore()
	ledger := NewMockLedger()
	order := &domain.Order{ID: "ord_rel", ServiceID: "s", QuoteNonce: "n", State: domain.OrderFailed}
	_ = store.Create(context.Background(), order, &domain.OrderTransition{OrderID: "ord_rel", ToState: domain.OrderFailed, Reason: "seed"})
	order.State = domain.OrderReleased
	_ = store.SaveWithOutbox(context.Background(), order,
		&domain.OrderTransition{OrderID: "ord_rel", FromState: domain.OrderFailed, ToState: domain.OrderReleased, Reason: "seed"},
		&domain.OutboxEntry{
			OrderID: "ord_rel", Op: domain.OutboxRelease, Payload: map[string]any{},
		})

	d := NewDispatcher(store, ledger, quietLog())
	if _, err := d.DispatchOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !ledger.Released("ord_rel") {
		t.Fatal("expected release applied")
	}
}
