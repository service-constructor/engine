// Package saga implements the Service Constructor payment orchestrator: it
// drives an order through freeze → execute → capture/release under an explicit
// state machine, persisting each transition. The settlement primitive (Ledger)
// and the service provider (Executor) are external to this open-source module,
// so they are expressed as ports with mock implementations for local runs.
package saga

import (
	"context"
	"time"

	"github.com/nvsces/service-constructor/internal/domain"
)

// Ledger is the settlement primitive (white paper §10). The platform owns
// balances in an internal ledger; the saga calls these as a black box. All
// operations are idempotent on orderID so retries are safe.
type Ledger interface {
	// Freeze moves amount from the wallet's available balance into held.
	Freeze(ctx context.Context, req FreezeRequest) error
	// Capture debits held and credits the service receiving wallet (net) and
	// the platform wallet (fee).
	Capture(ctx context.Context, req CaptureRequest) error
	// Release returns held funds to the user's available balance (compensation).
	Release(ctx context.Context, req ReleaseRequest) error
}

// FreezeRequest holds the inputs for a freeze. WalletID is the user's source
// wallet; Amount/CurrencyID come from the signed quote.
type FreezeRequest struct {
	OrderID    string
	WalletID   string
	Amount     string
	CurrencyID int64
}

// CaptureRequest splits the captured amount between the service and platform.
type CaptureRequest struct {
	OrderID string
	// WalletID is the user's wallet whose held funds are captured (the source of
	// the debit). The ledger credits the service and platform from it.
	WalletID string
	// Net is credited to the service's receiving wallet; Fee to the platform.
	Net string
	Fee string
	// ReceivingWalletID is the service payout wallet for the order's currency.
	ReceivingWalletID string
	CurrencyID        int64
}

// ReleaseRequest returns a wallet's held funds for an order to available
// (compensation). WalletID identifies whose held funds to return.
type ReleaseRequest struct {
	OrderID    string
	WalletID   string
	CurrencyID int64
}

// ExecuteStatus is the provider's verdict for an execute call.
type ExecuteStatus string

const (
	ExecuteSuccess ExecuteStatus = "SUCCESS"
	ExecuteFailed  ExecuteStatus = "FAILED"
	// ExecutePending: the provider will finalize later via webhook.
	ExecutePending ExecuteStatus = "PENDING"
)

// ExecuteResult is the provider's response to an execute call.
type ExecuteResult struct {
	Status      ExecuteStatus
	ExternalRef string
	Reason      string
}

// Executor calls the service provider's executeUrl to deliver the service
// (white paper §9). Implementations wrap timeout, idempotent retries (by
// orderID) and a circuit breaker; the saga treats it as a black box.
type Executor interface {
	Execute(ctx context.Context, req ExecuteRequest) (ExecuteResult, error)
}

// ExecuteRequest is the payload posted to the provider (idempotent by OrderID).
type ExecuteRequest struct {
	ExecuteURL string
	OrderID    string
	ServiceID  string
	UserID     string
	Amount     string
	CurrencyID int64
	QuoteNonce string
	Metadata   map[string]any
}

// OrderStore persists orders and supports idempotency lookups. Transitions and
// outbox writes happen in one DB transaction in the Postgres implementation.
//
// Every state change is also recorded as an append-only row in the order
// transition trail, written in the same transaction as the order UPDATE so the
// audit log can never diverge from the order's current state (white paper §8).
type OrderStore interface {
	// Create inserts a new order and records its genesis transition
	// (nil from-state -> CREATED). Returns domain.ErrAlreadyExists if an order
	// with the same id exists.
	Create(ctx context.Context, o *domain.Order, rec *domain.OrderTransition) error
	// Get loads an order by id.
	Get(ctx context.Context, id string) (*domain.Order, error)
	// FindByNonce returns the order created for a quote nonce, or
	// domain.ErrOrderNotFound if none — the basis of /pay idempotency.
	FindByNonce(ctx context.Context, serviceID, nonce string) (*domain.Order, error)
	// Save persists the order's current state (after a transition) and appends
	// the transition record, atomically.
	Save(ctx context.Context, o *domain.Order, rec *domain.OrderTransition) error
	// SaveWithOutbox persists the order's state, appends the transition record,
	// AND appends an outbox entry in a single transaction. This is how
	// capture/release are recorded: the order transition, its audit row, and the
	// pending ledger op commit atomically, closing the "ledger applied but order
	// not marked" gap (white paper section 11).
	SaveWithOutbox(ctx context.Context, o *domain.Order, rec *domain.OrderTransition, entry *domain.OutboxEntry) error
	// ListStuck returns orders in intermediate states whose freeze TTL elapsed
	// before olderThan, up to limit. The reconciler drives these to a final
	// state. EXECUTED (capture pending) and PENDING (awaiting webhook) qualify.
	ListStuck(ctx context.Context, olderThan time.Time, limit int) ([]*domain.Order, error)
	// ListTransitions returns an order's append-only audit trail in seq order.
	ListTransitions(ctx context.Context, orderID string) ([]*domain.OrderTransition, error)
}

// OutboxStore is the dispatcher's view of the outbox: read undispatched entries
// and mark them done. Separate from OrderStore so the dispatcher depends only on
// what it needs.
type OutboxStore interface {
	// ListUndispatched returns pending entries in insertion order, up to limit.
	ListUndispatched(ctx context.Context, limit int) ([]*domain.OutboxEntry, error)
	// MarkDispatched records that an entry's side-effect was applied.
	MarkDispatched(ctx context.Context, id int64) error
}

// StatusChecker queries the canonical order status from the service's statusUrl
// (white paper section 11.2, query-before-compensate). The reconciler consults
// it before any release so a lost execute response never triggers a blind
// refund of an actually-delivered service.
type StatusChecker interface {
	// CheckStatus returns the provider's verdict for an order. It returns
	// (Unknown, nil) when the provider cannot determine the status, in which
	// case the reconciler must not compensate.
	CheckStatus(ctx context.Context, statusURL, orderID string) (ProviderStatus, error)
}

// ProviderStatus is the canonical status reported by a service's statusUrl.
type ProviderStatus string

const (
	// ProviderDone: the service was delivered; the order must be captured.
	ProviderDone ProviderStatus = "DONE"
	// ProviderNotDone: the service was not delivered; safe to release.
	ProviderNotDone ProviderStatus = "NOT_DONE"
	// ProviderUnknown: status indeterminate; do NOT compensate.
	ProviderUnknown ProviderStatus = "UNKNOWN"
)
