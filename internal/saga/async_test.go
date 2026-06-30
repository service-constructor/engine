package saga

import (
	"context"
	"crypto/ed25519"
	"log/slog"
	"testing"
	"time"

	"github.com/nvsces/service-constructor/internal/domain"
)

// pendingOrder drives a fresh order to PENDING and returns the orchestrator,
// the order, the service, and the service private key (for signing callbacks).
func pendingOrder(t *testing.T) (*Orchestrator, *domain.Order, *domain.Service, ed25519.PrivateKey) {
	t.Helper()

	svcPubPEM, svcPriv := keypair(t)
	devPubPEM, devPriv := keypair(t)

	svc := &domain.Service{
		ID:               "svc_async",
		Name:             "eSIM async",
		ExecuteURL:       "https://svc/execute",
		StatusURL:        "https://svc/status",
		PublicKeys:       []domain.PublicKey{{KID: "k1", PEM: svcPubPEM}},
		ReceivingWallets: []domain.ReceivingWallet{{CurrencyID: 1, WalletID: "wlt_recv"}},
		Fee:              domain.Fee{Percent: "5"},
		Status:           domain.StatusActive,
	}

	q := Quote{
		Version: 1, ServiceID: svc.ID, UserID: "u_1", Amount: "10.00",
		CurrencyID: 1, AcceptedCurrencyIDs: []int64{1}, Description: "x",
		Nonce: "n-async", Exp: time.Now().Add(2 * time.Minute).Unix(), Kid: "k1",
	}
	qb, _ := canonicalQuoteBytes(q)
	q.Sig = signEd25519(svcPriv, qb)

	hash, _ := QuoteHash(q)
	c := Consent{QuoteHash: hash, WalletID: "wlt_user", Nonce: "cn", Ts: time.Now().Unix(), DeviceKid: "d1"}
	cb, _ := canonicalConsentBytes(c)
	c.Sig = signEd25519(devPriv, cb)

	orch := New(
		staticLookup{svc},
		NewMemOrderStore(),
		NewMockLedger(),
		&MockExecutor{Result: ExecuteResult{Status: ExecutePending, ExternalRef: "ref"}},
		StaticDeviceKeyResolver{PEM: devPubPEM},
		WithIDGen(func() string { return "ord_async" }),
	)

	order, err := orch.Pay(context.Background(), PayCommand{
		Quote: q, SelectedWalletID: "wlt_user", SelectedWalletCurrencyID: 1,
		Consent: c, AuthUserID: "u_1",
	})
	if err != nil {
		t.Fatalf("Pay: %v", err)
	}
	if order.State != domain.OrderPending {
		t.Fatalf("setup: state = %s, want PENDING", order.State)
	}
	return orch, order, svc, svcPriv
}

func signCallback(priv ed25519.PrivateKey, cb Callback) Callback {
	b, _ := canonicalCallbackBytes(cb)
	cb.Sig = signEd25519(priv, b)
	return cb
}

func TestCallbackSuccessCompletes(t *testing.T) {
	orch, order, _, priv := pendingOrder(t)
	cb := signCallback(priv, Callback{OrderID: order.ID, Status: "SUCCESS", ExternalRef: "ext", Kid: "k1"})

	got, err := orch.ProcessCallback(context.Background(), cb)
	if err != nil {
		t.Fatalf("ProcessCallback: %v", err)
	}
	if got.State != domain.OrderCompleted {
		t.Fatalf("state = %s, want COMPLETED", got.State)
	}
}

func TestCallbackFailedReleases(t *testing.T) {
	orch, order, _, priv := pendingOrder(t)
	cb := signCallback(priv, Callback{OrderID: order.ID, Status: "FAILED", Kid: "k1"})

	got, err := orch.ProcessCallback(context.Background(), cb)
	if err != nil {
		t.Fatalf("ProcessCallback: %v", err)
	}
	if got.State != domain.OrderReleased {
		t.Fatalf("state = %s, want RELEASED", got.State)
	}
}

func TestCallbackRejectsBadSignature(t *testing.T) {
	orch, order, _, _ := pendingOrder(t)
	// Sign with a different key.
	_, wrong := keypair(t)
	cb := signCallback(wrong, Callback{OrderID: order.ID, Status: "SUCCESS", Kid: "k1"})

	if _, err := orch.ProcessCallback(context.Background(), cb); err == nil {
		t.Fatal("expected signature rejection")
	}
}

func TestCallbackIdempotent(t *testing.T) {
	orch, order, _, priv := pendingOrder(t)
	cb := signCallback(priv, Callback{OrderID: order.ID, Status: "SUCCESS", Kid: "k1"})

	first, _ := orch.ProcessCallback(context.Background(), cb)
	second, err := orch.ProcessCallback(context.Background(), cb)
	if err != nil {
		t.Fatalf("second callback: %v", err)
	}
	if first.State != domain.OrderCompleted || second.State != domain.OrderCompleted {
		t.Fatalf("states = %s, %s; want both COMPLETED", first.State, second.State)
	}
}

func TestReconcilerQueryBeforeCompensate(t *testing.T) {
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(testWriter{t}, nil))

	t.Run("provider says DONE -> capture", func(t *testing.T) {
		orch, order, _, _ := pendingOrder(t)
		status := NewMockStatusChecker()
		status.Status = ProviderDone
		got, err := orch.ReconcileOrder(ctx, status, order)
		if err != nil {
			t.Fatal(err)
		}
		if got.State != domain.OrderCompleted {
			t.Fatalf("state = %s, want COMPLETED", got.State)
		}
	})

	t.Run("provider says NOT_DONE -> release", func(t *testing.T) {
		orch, order, _, _ := pendingOrder(t)
		status := NewMockStatusChecker()
		status.Status = ProviderNotDone
		got, err := orch.ReconcileOrder(ctx, status, order)
		if err != nil {
			t.Fatal(err)
		}
		if got.State != domain.OrderReleased {
			t.Fatalf("state = %s, want RELEASED", got.State)
		}
	})

	t.Run("provider UNKNOWN -> do not compensate", func(t *testing.T) {
		orch, order, _, _ := pendingOrder(t)
		status := NewMockStatusChecker() // defaults to UNKNOWN
		got, err := orch.ReconcileOrder(ctx, status, order)
		if err != nil {
			t.Fatal(err)
		}
		if got.State != domain.OrderPending {
			t.Fatalf("state = %s, want PENDING (untouched)", got.State)
		}
	})

	t.Run("reconciler loop finds stuck order", func(t *testing.T) {
		orch, order, _, _ := pendingOrder(t)
		status := NewMockStatusChecker()
		status.Status = ProviderDone
		// Run the order's freeze TTL into the past so ListStuck picks it up.
		rec := NewReconciler(orch, orch.orders, status, log,
			WithReconcilerClock(func() time.Time { return order.FreezeExpiresAt.Add(time.Minute) }))
		n, err := rec.ReconcileOnce(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("advanced = %d, want 1", n)
		}
	})
}

// testWriter adapts *testing.T to io.Writer for slog in tests.
type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Logf("%s", p)
	return len(p), nil
}
