-- 消息编辑版本号（乐观锁）：
-- 初始 0，每次成功编辑 +1；客户端编辑时必须回传读到的版本，版本不匹配则拒绝写入（409），
-- 避免多端并发编辑时后写者静默覆盖先写者的内容。DEFAULT 0 让存量消息可直接参与校验。
ALTER TABLE messages ADD COLUMN IF NOT EXISTS version BIGINT NOT NULL DEFAULT 0;

COMMENT ON COLUMN messages.version IS '消息编辑版本号：每次成功编辑自增，用于编辑请求的乐观锁校验';
