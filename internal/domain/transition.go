package domain

import "time"

// OrderTransition is one recorded edge of the saga state machine: the order
// moved from FromState to ToState for Reason. It is the audit unit of the
// payment saga — rows are append-only, written in the same transaction as the
// order state change, and never mutated (white paper §8).
type OrderTransition struct {
	ID      int64
	OrderID string
	// Seq is the order's 1-based transition counter (the first recorded row is 1).
	Seq int
	// FromState is empty for the initial row (order creation has no prior state).
	FromState OrderState
	ToState   OrderState
	// Reason is a short machine tag for why the edge was taken (e.g.
	// "execute_failed", "webhook_success").
	Reason string
	// Metadata carries optional structured context (provider ref, error detail).
	Metadata  map[string]any
	CreatedAt time.Time
}
