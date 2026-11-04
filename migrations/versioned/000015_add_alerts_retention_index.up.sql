-- Migration: 000015
-- Description: Index for alert retention cleanup.
-- 保留策略只删除已解决告警：WHERE resolved = true AND timestamp < ?。
-- 单列 timestamp 索引仍需回表过滤 resolved，数据量大时清理会扫过多无关行。

DO $$ BEGIN RAISE NOTICE '[Migration 000015] Adding alerts retention index...'; END $$;

CREATE INDEX IF NOT EXISTS idx_alerts_resolved_timestamp
    ON alerts(resolved, timestamp);
