-- Migration: 000001_add_role_to_messages
-- Description: Add role column to messages table
-- Version: 0.0.1

DO $$ BEGIN RAISE NOTICE '[Migration 000001] Adding role column to messages...'; END $$;

-- Add role column to messages table
ALTER TABLE messages ADD COLUMN IF NOT EXISTS role VARCHAR(50) DEFAULT 'user';

-- Create index for role column
CREATE INDEX IF NOT EXISTS idx_messages_role ON messages(role);

DO $$ BEGIN RAISE NOTICE '[Migration 000001] Migration completed!'; END $$;
