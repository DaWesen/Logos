-- Migration: 000001_add_role_to_messages (Rollback)
-- Description: Remove role column from messages table
-- Version: 0.0.1

DO $$ BEGIN RAISE NOTICE '[Migration 000001 - Rollback] Removing role column from messages...'; END $$;

-- Drop index first
DROP INDEX IF EXISTS idx_messages_role;

-- Drop the column
ALTER TABLE messages DROP COLUMN IF EXISTS role;

DO $$ BEGIN RAISE NOTICE '[Migration 000001 - Rollback] Rollback completed!'; END $$;
