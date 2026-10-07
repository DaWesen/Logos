-- Migration 000006 rollback
ALTER TABLE user_memories DROP COLUMN IF EXISTS evidence;
ALTER TABLE user_memories DROP COLUMN IF EXISTS conflict_value;
