-- Append-only audit log of saga state transitions. One row per accepted edge of
-- the order state machine, written in the SAME transaction as the order UPDATE so
-- the history can never diverge from the order's current state. Rows are only
-- ever INSERTed (never updated or deleted), giving a tamper-evident trail of how
-- each order reached its state — the audit view of the saga (white paper §8).
CREATE TABLE IF NOT EXISTS order_transitions (
    id          BIGSERIAL   PRIMARY KEY,
    order_id    TEXT        NOT NULL REFERENCES orders (order_id),
    -- seq is the order's own 1-based transition counter. (order_id, seq) is unique
    -- so replays/retries can't insert a duplicate step, and the trail is totally
    -- ordered per order without relying on wall-clock timestamps.
    seq         INT         NOT NULL,
    from_state  TEXT,       -- NULL for the initial CREATED row (no prior state)
    to_state    TEXT        NOT NULL,
    -- reason is a short machine tag for why the edge was taken (e.g. 'freeze_ok',
    -- 'execute_failed', 'webhook_success', 'reconcile'); optional context lives in
    -- metadata.
    reason      TEXT        NOT NULL DEFAULT '',
    metadata    JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at  TIMESTAMPTZ NOT NULL,

    CONSTRAINT order_transitions_to_check CHECK (to_state IN (
        'CREATED','FROZEN','EXECUTING','PENDING','EXECUTED',
        'COMPLETED','REJECTED','FAILED','RELEASED'
    ))
);

-- Enforce one row per (order, step): the audit trail is append-only and a given
-- transition is recorded exactly once even under retries.
CREATE UNIQUE INDEX IF NOT EXISTS order_transitions_order_seq_uniq
    ON order_transitions (order_id, seq);

-- Read the full trail for an order in transition order.
CREATE INDEX IF NOT EXISTS order_transitions_order_idx
    ON order_transitions (order_id, seq);
