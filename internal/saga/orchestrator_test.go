package saga

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"testing"
	"time"

	"github.com/nvsces/service-constructor/internal/domain"
)

// staticLookup returns a fixed service.
type staticLookup struct{ svc *domain.Service }

func (s staticLookup) Lookup(_ context.Context, id string) (*domain.Service, error) {
	if s.svc == nil || s.svc.ID != id {
		return nil, domain.ErrNotFound
	}
	return s.svc, nil
}

// keypair generates an Ed25519 pair and returns (publicPEM, privateKey).
func keypair(t *testing.T) (string, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	pemStr := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
	return pemStr, priv
}

func signEd25519(priv ed25519.PrivateKey, msg []byte) string {
	return base64.StdEncoding.EncodeToString(ed25519.Sign(priv, msg))
}

// buildSignedPay creates a service+device, a signed quote and a matching
// device-signed consent, and returns everything needed to drive the saga.
func buildSignedPay(t *testing.T) (*domain.Service, string, PayCommand) {
	t.Helper()

	svcPubPEM, svcPriv := keypair(t)
	devPubPEM, devPriv := keypair(t)

	svc := &domain.Service{
		ID:         "svc_1",
		Name:       "eSIM",
		ExecuteURL: "https://svc.example.com/execute",
		PublicKeys: []domain.PublicKey{{KID: "svc-2026", PEM: svcPubPEM}},
		ReceivingWallets: []domain.ReceivingWallet{
			{CurrencyID: 1, WalletID: "wlt_recv_usdt"},
		},
		Fee:    domain.Fee{Percent: "5"},
		Status: domain.StatusActive,
	}

	q := Quote{
		Version:             1,
		ServiceID:           "svc_1",
		UserID:              "u_42",
		Amount:              "5.00",
		CurrencyID:          1,
		AcceptedCurrencyIDs: []int64{1},
		Description:         "eSIM 5GB",
		Nonce:               "nonce-abc",
		Exp:                 time.Now().Add(2 * time.Minute).Unix(),
		Kid:                 "svc-2026",
	}
	qBytes, err := canonicalQuoteBytes(q)
	if err != nil {
		t.Fatal(err)
	}
	q.Sig = signEd25519(svcPriv, qBytes)

	hash, err := QuoteHash(q)
	if err != nil {
		t.Fatal(err)
	}
	consent := Consent{
		QuoteHash: hash,
		WalletID:  "wlt_user_usdt",
		Nonce:     "consent-xyz",
		Ts:        time.Now().Unix(),
		DeviceKid: "dev-1",
	}
	cBytes, err := canonicalConsentBytes(consent)
	if err != nil {
		t.Fatal(err)
	}
	consent.Sig = signEd25519(devPriv, cBytes)

	cmd := PayCommand{
		Quote:                    q,
		SelectedWalletID:         "wlt_user_usdt",
		Consent:                  consent,
		AuthUserID:               "u_42",
		SelectedWalletCurrencyID: 1,
	}
	return svc, devPubPEM, cmd
}

func newOrch(t *testing.T, svc *domain.Service, devPEM string, exec Executor, ledger Ledger) *Orchestrator {
	t.Helper()
	return New(
		staticLookup{svc},
		NewMemOrderStore(),
		ledger,
		exec,
		StaticDeviceKeyResolver{PEM: devPEM},
		WithIDGen(func() string { return "ord_test" }),
	)
}

func TestPayHappyPath(t *testing.T) {
	svc, devPEM, cmd := buildSignedPay(t)
	ledger := NewMockLedger()
	o := newOrch(t, svc, devPEM, NewMockExecutor(), ledger)

	order, err := o.Pay(context.Background(), cmd)
	if err != nil {
		t.Fatalf("Pay: %v", err)
	}
	if order.State != domain.OrderCompleted {
		t.Fatalf("state = %s, want COMPLETED", order.State)
	}
	if order.Net != "4.75" || order.Fee != "0.25" {
		t.Errorf("net=%s fee=%s, want net=4.75 fee=0.25", order.Net, order.Fee)
	}
	if _, ok := ledger.Captured(order.ID); !ok {
		t.Error("expected capture")
	}
	if ledger.Released(order.ID) {
		t.Error("must not release on success")
	}
}

func TestPayExecuteFailedCompensates(t *testing.T) {
	svc, devPEM, cmd := buildSignedPay(t)
	ledger := NewMockLedger()
	exec := &MockExecutor{Result: ExecuteResult{Status: ExecuteFailed, Reason: "out of stock"}}
	o := newOrch(t, svc, devPEM, exec, ledger)

	order, err := o.Pay(context.Background(), cmd)
	if err != nil {
		t.Fatalf("Pay: %v", err)
	}
	if order.State != domain.OrderReleased {
		t.Fatalf("state = %s, want RELEASED", order.State)
	}
	if !ledger.Released(order.ID) {
		t.Error("expected release on failure")
	}
	if _, ok := ledger.Captured(order.ID); ok {
		t.Error("must not capture on failure")
	}
}

func TestPayPendingParksOrder(t *testing.T) {
	svc, devPEM, cmd := buildSignedPay(t)
	exec := &MockExecutor{Result: ExecuteResult{Status: ExecutePending, ExternalRef: "ref"}}
	o := newOrch(t, svc, devPEM, exec, NewMockLedger())

	order, err := o.Pay(context.Background(), cmd)
	if err != nil {
		t.Fatalf("Pay: %v", err)
	}
	if order.State != domain.OrderPending {
		t.Fatalf("state = %s, want PENDING", order.State)
	}
}

func TestPayIdempotentOnNonce(t *testing.T) {
	svc, devPEM, cmd := buildSignedPay(t)
	o := newOrch(t, svc, devPEM, NewMockExecutor(), NewMockLedger())

	first, err := o.Pay(context.Background(), cmd)
	if err != nil {
		t.Fatalf("first Pay: %v", err)
	}
	second, err := o.Pay(context.Background(), cmd)
	if err != nil {
		t.Fatalf("second Pay: %v", err)
	}
	if first.ID != second.ID {
		t.Errorf("idempotency broken: %s != %s", first.ID, second.ID)
	}
}

func TestPayRejectsTamperedAmount(t *testing.T) {
	svc, devPEM, cmd := buildSignedPay(t)
	// Tamper after signing: signature no longer matches.
	cmd.Quote.Amount = "0.01"
	o := newOrch(t, svc, devPEM, NewMockExecutor(), NewMockLedger())

	_, err := o.Pay(context.Background(), cmd)
	if err == nil {
		t.Fatal("expected signature verification to fail")
	}
}

func TestPayRejectsUserMismatch(t *testing.T) {
	svc, devPEM, cmd := buildSignedPay(t)
	cmd.AuthUserID = "someone_else"
	o := newOrch(t, svc, devPEM, NewMockExecutor(), NewMockLedger())

	_, err := o.Pay(context.Background(), cmd)
	if err == nil {
		t.Fatal("expected user mismatch rejection")
	}
}

func TestPayRejectsExpiredQuote(t *testing.T) {
	svc, devPEM, cmd := buildSignedPay(t)
	o := newOrch(t, svc, devPEM, NewMockExecutor(), NewMockLedger())
	// Force the clock past the quote expiry.
	o.now = func() time.Time { return time.Unix(cmd.Quote.Exp+1, 0) }

	_, err := o.Pay(context.Background(), cmd)
	if err == nil {
		t.Fatal("expected expired-quote rejection")
	}
}
