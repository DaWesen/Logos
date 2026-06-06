-- Migration: 000003
-- Description: Add outbox table for reliable event publishing

DO $$ BEGIN RAISE NOTICE '[Migration 000003] Creating outbox table...'; END $$;

CREATE TABLE IF NOT EXISTS outbox_messages (
    id VARCHAR(36) PRIMARY KEY,
    topic VARCHAR(128) NOT NULL,
    key VARCHAR(256) NOT NULL,
    value JSONB NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    retry_count INTEGER NOT NULL DEFAULT 0,
    error_message TEXT,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    sent_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_outbox_status ON outbox_messages(status);
CREATE INDEX IF NOT EXISTS idx_outbox_topic ON outbox_messages(topic);
CREATE INDEX IF NOT EXISTS idx_outbox_created_at ON outbox_messages(created_at);
