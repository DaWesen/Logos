-- Migration: 000005
-- Description: Add next_retry_at to outbox for exponential backoff retry.
-- Failed messages now stay pending (retryable) with a backoff timestamp;
-- status 'failed' becomes a terminal dead-letter state after retries are exhausted.

DO $$ BEGIN RAISE NOTICE '[Migration 000005] Adding outbox retry backoff...'; END $$;

ALTER TABLE outbox_messages ADD COLUMN IF NOT EXISTS next_retry_at TIMESTAMPTZ;

-- 复合索引用于投递扫描：status + next_retry_at + created_at
CREATE INDEX IF NOT EXISTS idx_outbox_dispatch
    ON outbox_messages(status, next_retry_at, created_at);

-- 修复历史数据：此前失败的处于 failed 状态且未达重试上限的消息，恢复为 pending
UPDATE outbox_messages
SET status = 'pending'
WHERE status = 'failed' AND retry_count < 5;
