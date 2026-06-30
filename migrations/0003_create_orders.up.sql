-- Payment saga orders. Each row is the persistent unit of the orchestrator; the
-- state column is the saga state machine node.
CREATE TABLE IF NOT EXISTS orders (
    order_id          TEXT        PRIMARY KEY,
    service_id        TEXT        NOT NULL,
    user_id           TEXT        NOT NULL,
    wallet_id         TEXT        NOT NULL DEFAULT '',
    amount            TEXT        NOT NULL,
    currency_id       BIGINT      NOT NULL,
    quote_nonce       TEXT        NOT NULL,
    fee               TEXT        NOT NULL DEFAULT '0',
    net               TEXT        NOT NULL DEFAULT '0',
    external_ref      TEXT        NOT NULL DEFAULT '',
    metadata          JSONB       NOT NULL DEFAULT '{}'::jsonb,
    state             TEXT        NOT NULL,
    freeze_expires_at TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL,
    updated_at        TIMESTAMPTZ NOT NULL,

    CONSTRAINT orders_state_check CHECK (state IN (
        'CREATED','FROZEN','EXECUTING','PENDING','EXECUTED',
        'COMPLETED','REJECTED','FAILED','RELEASED'
    ))
);

-- /pay idempotency: at most one order per (service, quote nonce).
CREATE UNIQUE INDEX IF NOT EXISTS orders_service_nonce_uniq
    ON orders (service_id, quote_nonce);

-- Reconciler scans for orders stuck in intermediate states past their TTL.
CREATE INDEX IF NOT EXISTS orders_state_freeze_idx
    ON orders (state, freeze_expires_at);

-- Outbox: ledger/collector side-effects are written in the same transaction as
-- the order transition, then applied idempotently by a dispatcher. This closes
-- the "capture happened but order not marked" gap (white paper section 11).
CREATE TABLE IF NOT EXISTS outbox (
    id           BIGSERIAL   PRIMARY KEY,
    order_id     TEXT        NOT NULL,
    op           TEXT        NOT NULL,   -- FREEZE | CAPTURE | RELEASE | COLLECT
    payload      JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    dispatched_at TIMESTAMPTZ
);

-- Dispatcher polls undispatched rows in insertion order.
CREATE INDEX IF NOT EXISTS outbox_undispatched_idx
    ON outbox (id) WHERE dispatched_at IS NULL;
