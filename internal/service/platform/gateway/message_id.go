package gateway

import "github.com/google/uuid"

// DeriveMessageID 由 (senderID, requestID) 派生确定性消息 ID（UUIDv5）。
//
// 发送幂等的前提是「同一请求每次都映射到同一个消息 ID」：
//   - 客户端重发（WS 断线重连、TCP 重试、切到 REST 兜底）拿到的是同一个 ID；
//   - 落库按主键判重天然幂等，实时扇出重复帧也能被客户端按 ID 去重。
func DeriveMessageID(senderID, requestID string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("logos:chat:"+senderID+":"+requestID)).String()
}
