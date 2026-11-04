package types

import (
	"crypto/sha256"
	"encoding/hex"
)

// BotReplyMessageID 生成 Bot 回复的确定性消息 ID。
//
// 为什么不用随机 UUID：QQ 快速路径失败后会发布补偿事件，慢路径再调一次 Bot；
// Kafka 重投也可能让同一个入站事件被处理两次。随机 ID 会让两次回复变成两条
// 不同的消息，用户看到重复内容。用 (botID, 原消息ID) 派生 ID 后，重复回复
// 落到同一个主键上，配合 (sender_id, request_id) 幂等键只会保留一条。
//
// 返回空字符串表示输入不完整，调用方应退回随机 ID（而不是生成一个所有
// 空消息共享的 ID）。
func BotReplyMessageID(botID, sourceMessageID string) string {
	if botID == "" || sourceMessageID == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("bot_reply\x00" + botID + "\x00" + sourceMessageID))
	// messages.id 是 varchar(64)：前缀 4 + 32 位十六进制 = 36，留足余量。
	return "bot_" + hex.EncodeToString(sum[:16])
}
