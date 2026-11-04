-- 幂等键按 created_at 做保留期回收：
-- 没有索引时，每轮清理都要扫全表；有索引后可以先定位过期行，再按 LIMIT 分批删除。
CREATE INDEX IF NOT EXISTS idx_message_idempotency_created_at
    ON message_idempotency (created_at);
