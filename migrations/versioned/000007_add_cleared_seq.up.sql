-- 每个参与者独立的「清空历史」水位线：
-- 只隐藏该用户视角中 seq <= cleared_seq 的消息，不影响会话其他成员的历史。
ALTER TABLE conversation_participants ADD COLUMN IF NOT EXISTS cleared_seq BIGINT NOT NULL DEFAULT 0;

COMMENT ON COLUMN conversation_participants.cleared_seq IS '该用户已清空到的消息位点：seq <= cleared_seq 的消息不再对本人可见';
