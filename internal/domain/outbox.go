package domain

import "time"

// OutboxOp is a Ledger/Collector side-effect recorded transactionally alongside
// an order transition (white paper section 11, transactional outbox). A separate
// dispatcher applies these idempotently, so a crash between "order marked" and
// "ledger applied" cannot desynchronize money: the row survives and is retried.
type OutboxOp string

const (
	// OutboxCapture: debit held, credit the service (net) and platform (fee).
	OutboxCapture OutboxOp = "CAPTURE"
	// OutboxRelease: return held funds to the user (compensation).
	OutboxRelease OutboxOp = "RELEASE"
)

// OutboxEntry is one pending side-effect. Payload carries the operation inputs
// (decoded by the dispatcher). It is keyed by OrderID + Op so the dispatcher can
// apply it idempotently.
type OutboxEntry struct {
	ID           int64
	OrderID      string
	Op           OutboxOp
	Payload      map[string]any
	CreatedAt    time.Time
	DispatchedAt *time.Time
}
