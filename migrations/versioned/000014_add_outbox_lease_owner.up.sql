-- Migration: 000014
-- Description: Add lease_owner fencing token to outbox.
-- 租约到期后消息可能被另一个 relay 副本领取；旧副本的 MarkSent/MarkFailed
-- 必须能识别「这条消息已经不属于我」，否则会覆盖新副本的领取结果。

DO $$ BEGIN RAISE NOTICE '[Migration 000014] Adding outbox lease_owner...'; END $$;

ALTER TABLE outbox_messages ADD COLUMN IF NOT EXISTS lease_owner VARCHAR(64);

CREATE INDEX IF NOT EXISTS idx_outbox_lease_owner
    ON outbox_messages(lease_owner)
    WHERE lease_owner IS NOT NULL;
