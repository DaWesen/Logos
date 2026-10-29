-- 发送消息幂等键。
-- 唯一约束保证同一发送者 + 同一 request_id 只对应一条消息；
-- 与消息写入在同一事务内「先占位、再写消息」，竞争失败方直接回放首个请求产生的消息。
-- 记录可按 created_at 定期清理（例如保留 7 天），不影响历史消息。
CREATE TABLE IF NOT EXISTS message_idempotency (
    sender_id  VARCHAR(64) NOT NULL,
    request_id VARCHAR(64) NOT NULL,
    message_id VARCHAR(64) NOT NULL,
    created_at TIMESTAMP   NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (sender_id, request_id)
);
