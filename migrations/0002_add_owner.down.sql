DROP INDEX IF EXISTS services_owner_created_idx;
ALTER TABLE services DROP COLUMN IF EXISTS owner_id;
