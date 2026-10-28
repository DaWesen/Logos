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
)

type MessagingError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *MessagingError) Error() string {
	return e.Message
}
