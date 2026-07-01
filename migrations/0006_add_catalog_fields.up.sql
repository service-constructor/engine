-- Storefront/catalog display fields shown by the shell's app list. All public,
-- non-sensitive, and optional (empty by default).
ALTER TABLE services
    ADD COLUMN IF NOT EXISTS description TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS icon_url    TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS miniapp_url TEXT NOT NULL DEFAULT '';
