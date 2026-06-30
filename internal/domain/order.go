package domain

import (
	"errors"
	"time"
)

// OrderState is a node in the Service Constructor payment saga state machine.
//
// Happy path: CREATED -> FROZEN -> EXECUTING -> EXECUTED -> COMPLETED.
// Async execution branches through PENDING; failures compensate via RELEASED.
// See white paper section 8.
type OrderState string

const (
	// OrderCreated: quote verified and consent accepted; funds untouched.
	OrderCreated OrderState = "CREATED"
	// OrderFrozen: amount moved from available to held (Ledger.freeze).
	OrderFrozen OrderState = "FROZEN"
	// OrderExecuting: the provider executeUrl is being called.
	OrderExecuting OrderState = "EXECUTING"
	// OrderPending: async execution; awaiting the provider webhook.
	OrderPending OrderState = "PENDING"
	// OrderExecuted: service delivered; awaiting capture.
	OrderExecuted OrderState = "EXECUTED"
	// OrderCompleted: captured and recorded; terminal success.
	OrderCompleted OrderState = "COMPLETED"
	// OrderRejected: failed validation/consent; no freeze happened. Terminal.
	OrderRejected OrderState = "REJECTED"
	// OrderFailed: execution failed or timed out. Terminal.
	OrderFailed OrderState = "FAILED"
	// OrderReleased: held returned to the user (compensation). Terminal.
	OrderReleased OrderState = "RELEASED"
)

// Terminal reports whether a state never transitions further.
func (s OrderState) Terminal() bool {
	switch s {
	case OrderCompleted, OrderRejected, OrderFailed, OrderReleased:
		return true
	default:
		return false
	}
}

// allowedTransitions encodes the saga legal edges. Any edge not listed is
// rejected by CanTransition, which keeps the orchestrator honest and makes
// crash-recovery safe (an unexpected pair signals a bug, not a valid path).
var allowedTransitions = map[OrderState]map[OrderState]bool{
	OrderCreated: {
		OrderFrozen:   true, // freeze succeeded
		OrderRejected: true, // pre-freeze rejection (e.g. ledger refused)
	},
	OrderFrozen: {
		OrderExecuting: true, // begin provider call
		OrderReleased:  true, // user/system cancel before execute
	},
	OrderExecuting: {
		OrderExecuted: true, // synchronous success
		OrderPending:  true, // provider went async
		OrderFailed:   true, // synchronous failure (pre-capture)
		OrderReleased: true, // failure compensated immediately
	},
	OrderPending: {
		OrderExecuted: true, // webhook: success
		OrderFailed:   true, // webhook: failure
		OrderReleased: true, // failure compensated
	},
	OrderExecuted: {
		OrderCompleted: true, // capture succeeded
	},
	OrderFailed: {
		OrderReleased: true, // compensate a failed-but-frozen order
	},
}

// CanTransition reports whether moving from s to next is a legal saga edge.
func (s OrderState) CanTransition(next OrderState) bool {
	return allowedTransitions[s][next]
}

// Money is a decimal amount kept as a string to avoid float rounding in money
// math, paired with its currency.
type Money struct {
	Amount     string `json:"amount"`
	CurrencyID int64  `json:"currency_id"`
}

// Order is one payment processed by the saga. It is the persistent unit of the
// orchestrator and the record written to the collector on completion.
type Order struct {
	ID        string
	ServiceID string
	UserID    string
	// WalletID is the user source wallet selected on the consent screen.
	WalletID string
	// Amount and currency are fixed by the signed quote.
	Amount     string
	CurrencyID int64
	// QuoteNonce is the idempotency key carried by the quote; the pay handler is
	// idempotent on it.
	QuoteNonce string
	// Fee and Net split the captured amount between the platform and the
	// service receiving wallet (computed from the registry fee).
	Fee string
	Net string
	// ExternalRef is the provider reference for the executed service.
	ExternalRef string
	// Metadata is the opaque quote metadata echoed to the provider on execute.
	Metadata map[string]any
	State    OrderState
	// FreezeExpiresAt bounds how long held funds may sit before reconciliation
	// safely releases them (white paper section 8, freeze TTL).
	FreezeExpiresAt time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// Order-related sentinel errors.
var (
	ErrOrderNotFound       = errors.New("order not found")
	ErrInvalidTransition   = errors.New("invalid order state transition")
	ErrInvalidSignature    = errors.New("invalid signature")
	ErrQuoteExpired        = errors.New("quote expired")
	ErrConsentMismatch     = errors.New("consent does not match quote/wallet")
	ErrIdempotencyConflict = errors.New("idempotency conflict")
)

// Transition validates and applies a state change, returning ErrInvalidTransition
// for illegal edges so callers can surface a precise error.
func (o *Order) Transition(next OrderState) error {
	if !o.State.CanTransition(next) {
		return ErrInvalidTransition
	}
	o.State = next
	return nil
}
