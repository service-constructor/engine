package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/service-constructor/engine/internal/domain"
)

// OrderRepository persists saga orders. It implements saga.OrderStore.
type OrderRepository struct {
	pool *pgxpool.Pool
}

// NewOrderRepository wraps a pgx pool.
func NewOrderRepository(pool *pgxpool.Pool) *OrderRepository {
	return &OrderRepository{pool: pool}
}

const orderColumns = `order_id, service_id, user_id, wallet_id, amount, currency_id,
	quote_nonce, fee, net, external_ref, metadata, state, freeze_expires_at,
	created_at, updated_at`

func (r *OrderRepository) Create(ctx context.Context, o *domain.Order, rec *domain.OrderTransition) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) // no-op after Commit

	meta := marshalMeta(o.Metadata)
	_, err = tx.Exec(ctx, `
		INSERT INTO orders (`+orderColumns+`)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
		o.ID, o.ServiceID, o.UserID, o.WalletID, o.Amount, o.CurrencyID,
		o.QuoteNonce, o.Fee, o.Net, o.ExternalRef, meta, string(o.State),
		nullableTime(o.FreezeExpiresAt), o.CreatedAt, o.UpdatedAt,
	)
	if isUniqueViolation(err) {
		// Either the id or the (service_id, quote_nonce) unique index fired.
		return domain.ErrIdempotencyConflict
	}
	if err != nil {
		return fmt.Errorf("insert order: %w", err)
	}
	if err := insertTransition(ctx, tx, rec); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *OrderRepository) Get(ctx context.Context, id string) (*domain.Order, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+orderColumns+` FROM orders WHERE order_id = $1`, id)
	o, err := scanOrder(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrOrderNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get order: %w", err)
	}
	return o, nil
}

func (r *OrderRepository) FindByNonce(ctx context.Context, serviceID, nonce string) (*domain.Order, error) {
	row := r.pool.QueryRow(ctx,
		`SELECT `+orderColumns+` FROM orders WHERE service_id = $1 AND quote_nonce = $2`,
		serviceID, nonce)
	o, err := scanOrder(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrOrderNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find order by nonce: %w", err)
	}
	return o, nil
}

// ListByUser returns every order belonging to userID, newest first. It powers
// the personal cabinet's "My orders" view (orders across all mini-apps). Backed
// by the orders_user_created_idx index.
func (r *OrderRepository) ListByUser(ctx context.Context, userID string) ([]*domain.Order, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+orderColumns+` FROM orders
		WHERE user_id = $1
		ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list orders by user: %w", err)
	}
	defer rows.Close()

	var out []*domain.Order
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, fmt.Errorf("scan user order: %w", err)
		}
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate user orders: %w", err)
	}
	return out, nil
}

// pgExecutor is satisfied by both *pgxpool.Pool and pgx.Tx, so the order UPDATE
// can run either standalone or inside a transaction.
type pgExecutor interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

func saveOrder(ctx context.Context, db pgExecutor, o *domain.Order, rec *domain.OrderTransition) error {
	meta := marshalMeta(o.Metadata)
	tag, err := db.Exec(ctx, `
		UPDATE orders SET
			wallet_id = $2, amount = $3, currency_id = $4, fee = $5, net = $6,
			external_ref = $7, metadata = $8, state = $9, freeze_expires_at = $10,
			updated_at = $11
		WHERE order_id = $1`,
		o.ID, o.WalletID, o.Amount, o.CurrencyID, o.Fee, o.Net,
		o.ExternalRef, meta, string(o.State), nullableTime(o.FreezeExpiresAt), o.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("update order: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrOrderNotFound
	}
	return insertTransition(ctx, db, rec)
}

// insertTransition appends one append-only audit row. Seq is assigned inside the
// INSERT as (max existing seq for the order)+1, so it is monotonic per order and
// safe under the same transaction as the order state change. The
// (order_id, seq) unique index rejects any duplicate step.
func insertTransition(ctx context.Context, db pgExecutor, rec *domain.OrderTransition) error {
	meta := marshalMeta(rec.Metadata)
	var fromState *string
	if rec.FromState != "" {
		s := string(rec.FromState)
		fromState = &s
	}
	_, err := db.Exec(ctx, `
		INSERT INTO order_transitions
			(order_id, seq, from_state, to_state, reason, metadata, created_at)
		SELECT $1,
			COALESCE((SELECT MAX(seq) FROM order_transitions WHERE order_id = $1), 0) + 1,
			$2, $3, $4, $5, $6`,
		rec.OrderID, fromState, string(rec.ToState), rec.Reason, meta, rec.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert order transition: %w", err)
	}
	return nil
}

func (r *OrderRepository) Save(ctx context.Context, o *domain.Order, rec *domain.OrderTransition) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) // no-op after Commit
	if err := saveOrder(ctx, tx, o, rec); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// SaveWithOutbox persists the order transition, its audit row, and the outbox
// entry in one transaction, so they commit atomically (white paper section 11).
func (r *OrderRepository) SaveWithOutbox(ctx context.Context, o *domain.Order, rec *domain.OrderTransition, entry *domain.OutboxEntry) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) // no-op after Commit

	if err := saveOrder(ctx, tx, o, rec); err != nil {
		return err
	}
	payload, err := json.Marshal(entry.Payload)
	if err != nil {
		return fmt.Errorf("marshal outbox payload: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO outbox (order_id, op, payload) VALUES ($1,$2,$3)`,
		entry.OrderID, string(entry.Op), payload,
	); err != nil {
		return fmt.Errorf("insert outbox: %w", err)
	}
	return tx.Commit(ctx)
}

// ListUndispatched returns pending outbox entries in insertion order.
func (r *OrderRepository) ListUndispatched(ctx context.Context, limit int) ([]*domain.OutboxEntry, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id, order_id, op, payload, created_at, dispatched_at
		FROM outbox WHERE dispatched_at IS NULL
		ORDER BY id LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list outbox: %w", err)
	}
	defer rows.Close()

	var out []*domain.OutboxEntry
	for rows.Next() {
		var (
			e            domain.OutboxEntry
			op           string
			payload      []byte
			dispatchedAt *time.Time
		)
		if err := rows.Scan(&e.ID, &e.OrderID, &op, &payload, &e.CreatedAt, &dispatchedAt); err != nil {
			return nil, fmt.Errorf("scan outbox: %w", err)
		}
		e.Op = domain.OutboxOp(op)
		e.DispatchedAt = dispatchedAt
		if len(payload) > 0 {
			if err := json.Unmarshal(payload, &e.Payload); err != nil {
				return nil, fmt.Errorf("decode outbox payload: %w", err)
			}
		}
		out = append(out, &e)
	}
	return out, rows.Err()
}

// MarkDispatched records that an outbox entry's side-effect was applied.
func (r *OrderRepository) MarkDispatched(ctx context.Context, id int64) error {
	_, err := r.pool.Exec(ctx, `UPDATE outbox SET dispatched_at = now() WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("mark dispatched: %w", err)
	}
	return nil
}

// ListStuck returns orders in intermediate states (PENDING awaiting a webhook,
// EXECUTED awaiting capture) whose freeze TTL elapsed before olderThan.
func (r *OrderRepository) ListStuck(ctx context.Context, olderThan time.Time, limit int) ([]*domain.Order, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.pool.Query(ctx, `
		SELECT `+orderColumns+` FROM orders
		WHERE state IN ('PENDING','EXECUTED')
		  AND (freeze_expires_at IS NULL OR freeze_expires_at <= $1)
		ORDER BY freeze_expires_at NULLS FIRST
		LIMIT $2`, olderThan, limit)
	if err != nil {
		return nil, fmt.Errorf("list stuck orders: %w", err)
	}
	defer rows.Close()

	var out []*domain.Order
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, fmt.Errorf("scan stuck order: %w", err)
		}
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate stuck orders: %w", err)
	}
	return out, nil
}

// ListTransitions returns an order's append-only audit trail in seq order.
func (r *OrderRepository) ListTransitions(ctx context.Context, orderID string) ([]*domain.OrderTransition, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, order_id, seq, from_state, to_state, reason, metadata, created_at
		FROM order_transitions WHERE order_id = $1 ORDER BY seq`, orderID)
	if err != nil {
		return nil, fmt.Errorf("list transitions: %w", err)
	}
	defer rows.Close()

	var out []*domain.OrderTransition
	for rows.Next() {
		var (
			t         domain.OrderTransition
			fromState *string
			toState   string
			meta      []byte
		)
		if err := rows.Scan(&t.ID, &t.OrderID, &t.Seq, &fromState, &toState, &t.Reason, &meta, &t.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan transition: %w", err)
		}
		if fromState != nil {
			t.FromState = domain.OrderState(*fromState)
		}
		t.ToState = domain.OrderState(toState)
		if len(meta) > 0 {
			if err := json.Unmarshal(meta, &t.Metadata); err != nil {
				return nil, fmt.Errorf("decode transition metadata: %w", err)
			}
		}
		out = append(out, &t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate transitions: %w", err)
	}
	return out, nil
}

func scanOrder(row interface{ Scan(...any) error }) (*domain.Order, error) {
	var (
		o         domain.Order
		meta      []byte
		state     string
		freezeExp *time.Time
	)
	if err := row.Scan(
		&o.ID, &o.ServiceID, &o.UserID, &o.WalletID, &o.Amount, &o.CurrencyID,
		&o.QuoteNonce, &o.Fee, &o.Net, &o.ExternalRef, &meta, &state,
		&freezeExp, &o.CreatedAt, &o.UpdatedAt,
	); err != nil {
		return nil, err
	}
	o.State = domain.OrderState(state)
	if freezeExp != nil {
		o.FreezeExpiresAt = *freezeExp
	}
	if len(meta) > 0 {
		if err := json.Unmarshal(meta, &o.Metadata); err != nil {
			return nil, fmt.Errorf("decode metadata: %w", err)
		}
	}
	return &o, nil
}

func marshalMeta(m map[string]any) []byte {
	if m == nil {
		return []byte("{}")
	}
	b, err := json.Marshal(m)
	if err != nil {
		return []byte("{}")
	}
	return b
}

func nullableTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
