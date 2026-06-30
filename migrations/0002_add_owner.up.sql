-- Scope services to an owning account. Existing rows get a sentinel owner so
-- they remain visible to super-admins (and assignable later).
ALTER TABLE services
    ADD COLUMN IF NOT EXISTS owner_id TEXT NOT NULL DEFAULT '';

-- List queries filter by owner and order by (created_at, service_id); index the
-- owner-scoped access path.
CREATE INDEX IF NOT EXISTS services_owner_created_idx
    ON services (owner_id, created_at, service_id);
