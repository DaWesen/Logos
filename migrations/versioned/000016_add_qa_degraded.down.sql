-- Migration 000016 rollback
DROP INDEX IF EXISTS idx_qa_records_degraded;
ALTER TABLE qa_records DROP COLUMN IF EXISTS degraded;
