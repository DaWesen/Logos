-- 原位变更日志（编辑 / 撤回）。
-- 消息 seq 只表达「有新消息」；编辑与撤回会改写旧消息内容但不改变 seq，
-- 因此离线设备仅按 last_seq 补拉会一直停留在旧内容。每次原位变更追加一行，
-- 自增 ID 即 updated_seq，客户端按 (chat_id, updated_seq) 增量补拉，再按 version 幂等应用。
CREATE TABLE IF NOT EXISTS message_changes (
    id          BIGSERIAL PRIMARY KEY,
    chat_id     VARCHAR(100) NOT NULL,
    message_id  VARCHAR(64)  NOT NULL,
    change_type VARCHAR(16)  NOT NULL,
    content     TEXT,
    version     BIGINT       NOT NULL DEFAULT 0,
    message_seq BIGINT       NOT NULL DEFAULT 0,
    actor_id    VARCHAR(64),
    created_at  TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- 同步查询的形态是「某会话 + id > 游标，按 id 升序」，复合索引可直接覆盖。
CREATE INDEX IF NOT EXISTS idx_change_chat ON message_changes (chat_id, id);

COMMENT ON TABLE message_changes IS '原位变更日志：编辑/撤回不改变消息 seq，客户端用独立 updated_seq 游标补拉';
COMMENT ON COLUMN message_changes.message_seq IS '被变更消息的会话内 seq，用于跳过本人已清空的历史';
COMMENT ON COLUMN message_changes.version IS '变更后的消息版本，客户端仅在版本比本地新时应用';
