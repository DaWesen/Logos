-- Migration 000014 rollback
DROP INDEX IF EXISTS idx_outbox_lease_owner;
ALTER TABLE outbox_messages DROP COLUMN IF EXISTS lease_owner;
