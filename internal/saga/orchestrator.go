package saga

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/nvsces/service-constructor/internal/domain"
)

// ServiceLookup loads a registered service for orchestration. Payment is a
// system-initiated flow, so this lookup is unscoped (no per-account ownership).
type ServiceLookup interface {
	Lookup(ctx context.Context, serviceID string) (*domain.Service, error)
}

// DeviceKeyResolver returns the PEM public key for a device kid so the platform
// can verify the device-signed consent. In a real wallet this comes from the
// device-registration service; for local runs a static resolver is used.
type DeviceKeyResolver interface {
	DevicePublicKeyPEM(ctx context.Context, userID, deviceKid string) (string, error)
}

// Clock and IDGen mirror the registry's small seams for deterministic tests.
type Clock func() time.Time
type IDGen func() string

// Orchestrator drives the payment saga.
type Orchestrator struct {
	services ServiceLookup
	orders   OrderStore
	ledger   Ledger
	executor Executor
	devices  DeviceKeyResolver
	now      Clock
	newID    IDGen
	// freezeTTL bounds how long held funds may sit before reconciliation.
	freezeTTL time.Duration
}

// Option configures an Orchestrator.
type Option func(*Orchestrator)

func WithClock(c Clock) Option { return func(o *Orchestrator) { o.now = c } }
func WithIDGen(g IDGen) Option { return func(o *Orchestrator) { o.newID = g } }
func WithFreezeTTL(d time.Duration) Option {
	return func(o *Orchestrator) { o.freezeTTL = d }
}

// New builds an Orchestrator.
func New(services ServiceLookup, orders OrderStore, ledger Ledger, executor Executor, devices DeviceKeyResolver, opts ...Option) *Orchestrator {
	o := &Orchestrator{
		services:  services,
		orders:    orders,
		ledger:    ledger,
		executor:  executor,
		devices:   devices,
		now:       time.Now,
		newID:     defaultOrderID,
		freezeTTL: 2 * time.Minute,
	}
	for _, opt := range opts {
		opt(o)
	}
	return o
}

func defaultOrderID() string {
	return "ord_" + strings.ReplaceAll(uuid.NewString(), "-", "")
}

// Pay validates the command and runs the saga, returning the resulting order.
// It is idempotent on the quote nonce: a repeated call for the same nonce
// returns the existing order instead of charging twice.
func (o *Orchestrator) Pay(ctx context.Context, cmd PayCommand) (*domain.Order, error) {
	// Idempotency: if an order already exists for this quote nonce, return it.
	if existing, err := o.orders.FindByNonce(ctx, cmd.Quote.ServiceID, cmd.Quote.Nonce); err == nil {
		return existing, nil
	}

	svc, err := o.services.Lookup(ctx, cmd.Quote.ServiceID)
	if err != nil {
		return nil, err
	}

	if err := o.validate(ctx, cmd, svc); err != nil {
		return nil, err
	}

	net, fee, err := splitFee(cmd.Quote.Amount, svc.Fee)
	if err != nil {
		return nil, err
	}

	now := o.now().UTC()
	order := &domain.Order{
		ID:              o.newID(),
		ServiceID:       svc.ID,
		UserID:          cmd.Quote.UserID,
		WalletID:        cmd.SelectedWalletID,
		Amount:          cmd.Quote.Amount,
		CurrencyID:      cmd.Quote.CurrencyID,
		QuoteNonce:      cmd.Quote.Nonce,
		Fee:             fee,
		Net:             net,
		Metadata:        cmd.Quote.Metadata,
		State:           domain.OrderCreated,
		FreezeExpiresAt: now.Add(o.freezeTTL),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := o.orders.Create(ctx, order); err != nil {
		// A concurrent request may have created it first — fall back to it.
		if existing, ferr := o.orders.FindByNonce(ctx, cmd.Quote.ServiceID, cmd.Quote.Nonce); ferr == nil {
			return existing, nil
		}
		return nil, err
	}

	return o.run(ctx, order, svc)
}

// validate enforces the /pay invariants (white paper §7.3) and verifies the
// quote and consent signatures.
func (o *Orchestrator) validate(ctx context.Context, cmd PayCommand, svc *domain.Service) error {
	q := cmd.Quote

	// userId must match the authenticated session.
	if cmd.AuthUserID == "" || q.UserID != cmd.AuthUserID {
		return fmt.Errorf("%w: quote userId does not match session", domain.ErrInvalidArgument)
	}

	// Quote not expired.
	if q.Exp != 0 && o.now().Unix() > q.Exp {
		return domain.ErrQuoteExpired
	}

	// Selected wallet currency must be accepted by the service, and must match
	// the quote currency (no blind conversion).
	if cmd.SelectedWalletCurrencyID != q.CurrencyID {
		return fmt.Errorf("%w: wallet currency differs from quote currency", domain.ErrInvalidArgument)
	}
	if !acceptsCurrency(svc, cmd.SelectedWalletCurrencyID) {
		return fmt.Errorf("%w: currency not accepted by service", domain.ErrInvalidArgument)
	}

	// Quote signature (service key from registry).
	if err := VerifyQuoteSignature(q, svc); err != nil {
		return err
	}

	// Consent must bind to this exact quote and the selected wallet.
	wantHash, err := QuoteHash(q)
	if err != nil {
		return err
	}
	if cmd.Consent.QuoteHash != wantHash || cmd.Consent.WalletID != cmd.SelectedWalletID {
		return domain.ErrConsentMismatch
	}

	// Consent signature (device key).
	devPEM, err := o.devices.DevicePublicKeyPEM(ctx, q.UserID, cmd.Consent.DeviceKid)
	if err != nil {
		return fmt.Errorf("%w: device key: %v", domain.ErrInvalidSignature, err)
	}
	if err := VerifyConsentSignature(cmd.Consent, devPEM); err != nil {
		return err
	}
	return nil
}

// acceptsCurrency reports whether the service has a receiving wallet for the
// currency (the set of receiving wallets defines accepted currencies).
func acceptsCurrency(svc *domain.Service, currencyID int64) bool {
	for _, w := range svc.ReceivingWallets {
		if w.CurrencyID == currencyID {
			return true
		}
	}
	return false
}

func receivingWalletFor(svc *domain.Service, currencyID int64) string {
	for _, w := range svc.ReceivingWallets {
		if w.CurrencyID == currencyID {
			return w.WalletID
		}
	}
	return ""
}

// run executes the saga from a freshly created order: freeze → execute →
// capture, compensating with release on failure. Every transition is persisted
// so the flow is recoverable after a crash.
func (o *Orchestrator) run(ctx context.Context, order *domain.Order, svc *domain.Service) (*domain.Order, error) {
	// 1. Freeze: move funds into held BEFORE calling execute (key invariant).
	if err := o.ledger.Freeze(ctx, FreezeRequest{
		OrderID:    order.ID,
		WalletID:   order.WalletID,
		Amount:     order.Amount,
		CurrencyID: order.CurrencyID,
	}); err != nil {
		_ = o.transition(ctx, order, domain.OrderRejected)
		return order, fmt.Errorf("freeze: %w", err)
	}
	if err := o.transition(ctx, order, domain.OrderFrozen); err != nil {
		return order, err
	}

	// 2. Execute: call the provider.
	if err := o.transition(ctx, order, domain.OrderExecuting); err != nil {
		return order, err
	}
	res, err := o.executor.Execute(ctx, ExecuteRequest{
		ExecuteURL: svc.ExecuteURL,
		OrderID:    order.ID,
		ServiceID:  order.ServiceID,
		UserID:     order.UserID,
		Amount:     order.Amount,
		CurrencyID: order.CurrencyID,
		QuoteNonce: order.QuoteNonce,
		Metadata:   order.Metadata,
	})
	if err != nil {
		// Transport/unknown failure: treat as failed and compensate.
		return o.fail(ctx, order)
	}

	switch res.Status {
	case ExecutePending:
		// Async: park the order. The provider webhook (HandleCallback) or the
		// reconciler finalizes it later.
		_ = o.transition(ctx, order, domain.OrderPending)
		return order, nil
	case ExecuteFailed:
		return o.fail(ctx, order)
	case ExecuteSuccess:
		order.ExternalRef = res.ExternalRef
		if err := o.transition(ctx, order, domain.OrderExecuted); err != nil {
			return order, err
		}
		return o.capture(ctx, order, svc)
	default:
		return o.fail(ctx, order)
	}
}

// capture transitions the order to COMPLETED and records the capture as an
// outbox entry in the SAME transaction. The actual Ledger.Capture is applied
// later by the dispatcher, idempotently. Because the order is only marked
// COMPLETED if the outbox row also commits, money and order state cannot
// desynchronize (white paper section 11).
func (o *Orchestrator) capture(ctx context.Context, order *domain.Order, svc *domain.Service) (*domain.Order, error) {
	entry := &domain.OutboxEntry{
		OrderID: order.ID,
		Op:      domain.OutboxCapture,
		Payload: map[string]any{
			"net":               order.Net,
			"fee":               order.Fee,
			"receivingWalletId": receivingWalletFor(svc, order.CurrencyID),
			"currencyId":        order.CurrencyID,
		},
	}
	if err := o.transitionWithOutbox(ctx, order, domain.OrderCompleted, entry); err != nil {
		return order, err
	}
	return order, nil
}

// fail compensates a frozen order: it moves to FAILED, then to RELEASED with a
// release outbox entry committed atomically.
func (o *Orchestrator) fail(ctx context.Context, order *domain.Order) (*domain.Order, error) {
	if err := o.transition(ctx, order, domain.OrderFailed); err != nil {
		return order, err
	}
	entry := &domain.OutboxEntry{
		OrderID: order.ID,
		Op:      domain.OutboxRelease,
		Payload: map[string]any{},
	}
	if err := o.transitionWithOutbox(ctx, order, domain.OrderReleased, entry); err != nil {
		return order, err
	}
	return order, nil
}

// transition applies a state change and persists it.
func (o *Orchestrator) transition(ctx context.Context, order *domain.Order, next domain.OrderState) error {
	if err := order.Transition(next); err != nil {
		return err
	}
	order.UpdatedAt = o.now().UTC()
	return o.orders.Save(ctx, order)
}

// transitionWithOutbox applies a state change and appends an outbox entry in one
// transaction.
func (o *Orchestrator) transitionWithOutbox(ctx context.Context, order *domain.Order, next domain.OrderState, entry *domain.OutboxEntry) error {
	if err := order.Transition(next); err != nil {
		return err
	}
	order.UpdatedAt = o.now().UTC()
	return o.orders.SaveWithOutbox(ctx, order, entry)
}

// ProcessCallback verifies a signed provider webhook and finalizes the order.
// The signature is checked against the service registered for the order, so a
// forged callback cannot complete or fail someone else's payment.
func (o *Orchestrator) ProcessCallback(ctx context.Context, cb Callback) (*domain.Order, error) {
	order, err := o.orders.Get(ctx, cb.OrderID)
	if err != nil {
		return nil, err
	}
	svc, err := o.services.Lookup(ctx, order.ServiceID)
	if err != nil {
		return nil, err
	}
	if err := VerifyCallbackSignature(cb, svc); err != nil {
		return nil, err
	}
	// Already terminal (idempotent / late delivery): no-op.
	if order.State.Terminal() {
		return order, nil
	}
	if order.State != domain.OrderPending {
		return nil, fmt.Errorf("%w: order %s is %s, not PENDING", domain.ErrInvalidTransition, cb.OrderID, order.State)
	}
	if cb.ExternalRef != "" {
		order.ExternalRef = cb.ExternalRef
	}
	return o.finalize(ctx, order, svc, cb.Success())
}

// ReconcileOrder drives a single stuck order toward a final state, applying
// query-before-compensate (white paper section 11.2): it never releases blindly.
//
//   - EXECUTED (capture lost): retry capture to COMPLETED. Funds are already
//     held, so settlement is guaranteed.
//   - PENDING (execute/webhook lost): ask the provider's statusUrl. DONE -> finalize
//     success; NOT_DONE -> finalize failure (release); UNKNOWN -> leave untouched.
//
// It is a no-op for already-terminal orders.
func (o *Orchestrator) ReconcileOrder(ctx context.Context, status StatusChecker, order *domain.Order) (*domain.Order, error) {
	if order.State.Terminal() {
		return order, nil
	}
	svc, err := o.services.Lookup(ctx, order.ServiceID)
	if err != nil {
		return nil, err
	}

	switch order.State {
	case domain.OrderExecuted:
		// Service confirmed; only the capture is missing. Retry it.
		return o.capture(ctx, order, svc)

	case domain.OrderPending:
		st, err := status.CheckStatus(ctx, svc.StatusURL, order.ID)
		if err != nil {
			return order, fmt.Errorf("status check: %w", err)
		}
		switch st {
		case ProviderDone:
			return o.finalize(ctx, order, svc, true)
		case ProviderNotDone:
			return o.finalize(ctx, order, svc, false)
		default:
			// UNKNOWN: do not compensate. Leave for a later pass / operator.
			return order, nil
		}

	default:
		// Other non-terminal states (FROZEN/EXECUTING) are transient within a
		// single Pay call; the reconciler does not touch them here.
		return order, nil
	}
}

// finalize drives a PENDING order to its terminal state: success captures and
// completes; failure releases held funds. Shared by the webhook and reconciler.
func (o *Orchestrator) finalize(ctx context.Context, order *domain.Order, svc *domain.Service, success bool) (*domain.Order, error) {
	if !success {
		return o.fail(ctx, order)
	}
	if err := o.transition(ctx, order, domain.OrderExecuted); err != nil {
		return order, err
	}
	return o.capture(ctx, order, svc)
}
