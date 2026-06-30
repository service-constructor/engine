CREATE TABLE IF NOT EXISTS services (
    service_id        TEXT        PRIMARY KEY,
    name              TEXT        NOT NULL,
    public_keys       JSONB       NOT NULL DEFAULT '[]'::jsonb,
    origins           TEXT[]      NOT NULL DEFAULT '{}',
    execute_url       TEXT        NOT NULL DEFAULT '',
    status_url        TEXT        NOT NULL DEFAULT '',
    receiving_wallets JSONB       NOT NULL DEFAULT '[]'::jsonb,
    fee               JSONB       NOT NULL DEFAULT '{}'::jsonb,
    limits            JSONB       NOT NULL DEFAULT '{}'::jsonb,
    status            TEXT        NOT NULL DEFAULT 'draft',
    created_at        TIMESTAMPTZ NOT NULL,
    updated_at        TIMESTAMPTZ NOT NULL,

    CONSTRAINT services_status_check
        CHECK (status IN ('draft', 'active', 'suspended'))
);

-- Supports keyset pagination ordered by (created_at, service_id) and status filter.
CREATE INDEX IF NOT EXISTS services_status_created_idx
    ON services (status, created_at, service_id);

CREATE INDEX IF NOT EXISTS services_created_idx
    ON services (created_at, service_id);
