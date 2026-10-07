-- Migration 000005 rollback
DROP INDEX IF EXISTS idx_outbox_dispatch;
ALTER TABLE outbox_messages DROP COLUMN IF EXISTS next_retry_at;
