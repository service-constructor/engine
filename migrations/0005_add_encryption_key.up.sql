-- The service's X25519 public key (base64 raw) for sealed-box encrypting the
-- user id the shell hands the mini-app. Distinct from public_keys (Ed25519
-- signature keys); empty means the service opted out of encrypted user context.
ALTER TABLE services
    ADD COLUMN IF NOT EXISTS encryption_public_key TEXT NOT NULL DEFAULT '';
