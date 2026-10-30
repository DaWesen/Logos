package downlink

import "strings"

// Sender 是所有下行投递方式的最小集合，*gateway.UnifiedConnectionManager 天然满足。
// 抽成接口的意图：路由规则可以脱离 Redis / 连接管理器做单测。
type Sender interface {
	SendToUser(userID string, data []byte)
	SendToUsers(userIDs []string, data []byte)
	BroadcastMessage(data []byte)
	BroadcastMessageExcept(data []byte, exceptUserID string)
}

// PrivateChatPeer 解析私聊会话 ID 并返回「除 actor 之外的另一个参与者」。
//
// 支持的格式：
//   - private_{a}_{b}（前端与 Chat 服务实际使用的格式）
//   - {a}_{b}（历史格式，两端均为数字 ID）
//
// private_{id}_self 是 QQ 单边会话，没有对端，返回 false。
// actor 不是会话参与者时也返回 false（调用方应直接丢弃，而不是退回广播）。
func PrivateChatPeer(chatID, actorID string) (string, bool) {
	parts := strings.Split(chatID, "_")

	var a, b string
	switch {
	case len(parts) == 3 && parts[0] == "private":
		a, b = parts[1], parts[2]
	case len(parts) == 2 && isDigits(parts[0]) && isDigits(parts[1]):
		a, b = parts[0], parts[1]
	default:
		return "", false
	}

	if a == "" || b == "" || b == "self" {
		return "", false
	}

	switch actorID {
	case a:
		return b, true
	case b:
		return a, true
	default:
		return "", false
	}
}

// IsPrivateChatID 判断会话 ID 是否是私聊格式（与会话参与者是谁无关）。
func IsPrivateChatID(chatID string) bool {
	parts := strings.Split(chatID, "_")
	if len(parts) == 3 && parts[0] == "private" {
		return true
	}
	return len(parts) == 2 && isDigits(parts[0]) && isDigits(parts[1])
}

// IsBroadcastChatID 判断会话 ID 是否属于全站广播（系统公告）。
// 这类会话没有成员名单也理应发给所有人，是唯一允许「缺少接收者时退化为全站广播」的场景。
func IsBroadcastChatID(chatID string) bool {
	return chatID == "broadcast" || strings.HasPrefix(chatID, "broadcast_")
}

// hasRealRecipient 判断名单里是否存在非空用户 ID。
//
// 上游可能传来 []string{""}（名单字段存在但内容为空，常见于 JSON 反序列化的空数组元素）。
// 只看 len(recipients) 会把它当成「有精确名单」，于是事件既不投递也不走兜底分支，
// 最终静默丢失；这里统一按「没有名单」处理，让后续兜底/丢弃规则正常生效。
func hasRealRecipient(recipients []string) bool {
	for _, r := range recipients {
		if r != "" {
			return true
		}
	}
	return false
}

// DeliverToUsers 精确投递；recipients 为空（或只有空 ID）时什么也不做，返回 false。
// 用于「只应该发给指定的人」的事件（编辑、删除）：这些事件绝不能退化成广播。
func DeliverToUsers(s Sender, recipients []string, data []byte) bool {
	if !hasRealRecipient(recipients) {
		return false
	}
	s.SendToUsers(recipients, data)
	return true
}

// DeliverMessage 聊天消息的投递规则。与 DeliverToChat 的唯一差别：私聊兜底时同时投给
// 发送者本人（发送者的其他设备也要看到自己刚发的消息），而不是只发对端。
//
// allowBroadcast 由调用方按事件类型给出（ChatTypeBroadcast）。缺少接收者时只有广播会话
// 可以退化为全站投递；群聊缺少成员名单必须丢弃——否则一条群消息会泄露给全站在线用户。
func DeliverMessage(s Sender, data []byte, recipients []string, senderID, chatID string, allowBroadcast bool) bool {
	if hasRealRecipient(recipients) {
		s.SendToUsers(recipients, data)
		return true
	}

	if peer, ok := PrivateChatPeer(chatID, senderID); ok {
		s.SendToUsers([]string{senderID, peer}, data)
		return true
	}

	if IsPrivateChatID(chatID) {
		return false
	}

	if !allowBroadcast && !IsBroadcastChatID(chatID) {
		return false
	}

	s.BroadcastMessageExcept(data, senderID)
	return true
}

// DeliverToChat 按会话语义投递：
//  1. 有精确名单 → 精确投递；
//  2. 私聊 → 只投递给对端；
//  3. 广播会话 / 调用方声明为广播 → 发给除 actor 外的所有人；
//  4. 其余（私聊解析不出对端、群聊缺少成员名单）→ **丢弃**，绝不退化成全站广播。
//
// 返回 false 表示没有投递（调用方可以记录告警）。
func DeliverToChat(s Sender, data []byte, recipients []string, actorID, chatID string, allowBroadcast bool) bool {
	if hasRealRecipient(recipients) {
		s.SendToUsers(recipients, data)
		return true
	}

	if peer, ok := PrivateChatPeer(chatID, actorID); ok {
		s.SendToUser(peer, data)
		return true
	}

	// 私聊却解析不出对端：宁可丢这一帧，也不能把私聊内容/输入状态/已读回执广播给全站。
	if IsPrivateChatID(chatID) {
		return false
	}

	if !allowBroadcast && !IsBroadcastChatID(chatID) {
		return false
	}

	s.BroadcastMessageExcept(data, actorID)
	return true
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
