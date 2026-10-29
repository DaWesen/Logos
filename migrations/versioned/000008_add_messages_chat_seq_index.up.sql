-- 未读数聚合与历史/增量拉取都以 (chat_id, seq) 为过滤条件；
-- 单列 idx_messages_chat_id 无法覆盖 seq 范围扫描，补复合索引避免回表放大。
CREATE INDEX IF NOT EXISTS idx_messages_chat_seq ON messages(chat_id, seq);
