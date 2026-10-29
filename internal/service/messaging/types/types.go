package types

type MessageType int

const (
	MessageTypeText MessageType = iota + 1
	MessageTypeImage
	MessageTypeFile
	MessageTypeVoice
	MessageTypeVideo
	MessageTypeLocation
	MessageTypeSystem
	MessageTypeEnd
)

func (m MessageType) String() string {
	switch m {
	case MessageTypeText:
		return "text"
	case MessageTypeImage:
		return "image"
	case MessageTypeFile:
		return "file"
	case MessageTypeVoice:
		return "voice"
	case MessageTypeVideo:
		return "video"
	case MessageTypeLocation:
		return "location"
	case MessageTypeSystem:
		return "system"
	default:
		return "unknown"
	}
}

type ChatType int

const (
	ChatTypePrivate ChatType = iota + 1
	ChatTypeGroup
	ChatTypeChannel
	ChatTypeBroadcast
	ChatTypeEnd
)

func (c ChatType) String() string {
	switch c {
	case ChatTypePrivate:
		return "private"
	case ChatTypeGroup:
		return "group"
	case ChatTypeChannel:
		return "channel"
	case ChatTypeBroadcast:
		return "broadcast"
	default:
		return "unknown"
	}
}

var (
	ErrContentRejected = &MessagingError{Code: 50405, Message: "content rejected by moderation"}
	// ErrContentModerationUnavailable 审核服务未能给出判定（模型不可用、输出无法解析、超时）。
	// 采用失败关闭策略：宁可拒绝发送，也不让未经审核的内容流入群聊。
	ErrContentModerationUnavailable = &MessagingError{Code: 50406, Message: "content moderation unavailable, please retry later"}
	// ErrChatAccessDenied 请求者无权访问目标会话（非群成员、非私聊参与方或无法解析会话）。
	// 历史、搜索、已读、清空等入口统一失败关闭，避免通过猜 chat_id 越权读取他人会话。
	ErrChatAccessDenied = &MessagingError{Code: 50403, Message: "无权访问该会话"}
	// ErrChatPermissionDenied 已通过会话访问校验，但当前角色无权执行该操作（如普通成员清空群历史）。
	ErrChatPermissionDenied = &MessagingError{Code: 50409, Message: "当前角色无权执行该操作"}
	// ErrChatBadRequest 请求参数不合法（缺字段、超出批量上限等），客户端修正请求后可重试。
	ErrChatBadRequest = &MessagingError{Code: 50400, Message: "请求参数不合法"}
	// ErrChatMessageNotFound 目标消息不存在，或请求者无权访问其所在会话。
	// 两种情况故意返回同一错误：否则攻击者可用错误码差异枚举他人会话里的 message_id。
	ErrChatMessageNotFound = &MessagingError{Code: 50404, Message: "消息不存在"}
	// ErrChatMessageConflict 编辑消息时客户端持有的版本已过期（其他端已先编辑成功）。
	// 返回 409 而非覆盖写入，客户端应重新拉取最新内容后再提交，避免并发编辑丢失更新。
	ErrChatMessageConflict = &MessagingError{Code: 50410, Message: "消息内容已变更，请刷新后重试"}
)

type MessagingError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *MessagingError) Error() string {
	return e.Message
}
