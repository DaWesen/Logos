-- Migration: 000006
-- Description: Add evidence & conflict fields to user_memories for
-- confidence lifecycle (evidence traceability + dual-conclusion on conflict)

DO $$ BEGIN RAISE NOTICE '[Migration 000006] Adding memory evidence/conflict...'; END $$;

ALTER TABLE user_memories ADD COLUMN IF NOT EXISTS evidence JSONB;
ALTER TABLE user_memories ADD COLUMN IF NOT EXISTS conflict_value TEXT;
