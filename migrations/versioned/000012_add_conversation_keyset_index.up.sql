-- 会话列表 keyset 分页的支撑索引。
--
-- 查询形态是：
--   SELECT ... FROM conversations
--   JOIN conversation_participants p ON p.conversation_id = conversations.id
--   WHERE p.user_id = ? AND (updated_at, id) < (?, ?)
--   ORDER BY updated_at DESC, id DESC LIMIT ?;
--
-- 1) 排序键复合索引：让 keyset 的定位与排序走同一个索引，
--    避免深分页时退化成全表排序。
CREATE INDEX IF NOT EXISTS idx_conversations_updated_at_id
    ON conversations (updated_at DESC, id DESC);

-- 2) JOIN 侧覆盖索引：按用户取会话时直接命中 (user_id, conversation_id)，
--    不必先按 user_id 找到行再回表取 conversation_id。
CREATE INDEX IF NOT EXISTS idx_conversation_participants_user_conv
    ON conversation_participants (user_id, conversation_id);
