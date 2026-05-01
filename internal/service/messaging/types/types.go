package types

// Messaging 领域共享类型
// 包含 IM、Chat、Contact、Message 服务共用的数据结构

// MessageType 消息类型
type MessageType int

const (
	MessageTypeText     MessageType = iota + 1 // 文本消息
	MessageTypeImage                           // 图片消息
	MessageTypeFile                            // 文件消息
	MessageTypeVoice                           // 语音消息
	MessageTypeVideo                           // 视频消息
	MessageTypeLocation                        // 位置消息
	MessageTypeSystem                          // 系统消息
	MessageTypeEnd                             // 边界标记，用于检查完整性
)

// String 实现 String 方法
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

// ChatType 会话类型
type ChatType int

const (
	ChatTypePrivate   ChatType = iota + 1 // 私聊
	ChatTypeGroup                         // 群聊
	ChatTypeChannel                       // 频道
	ChatTypeBroadcast                     // 广播
	ChatTypeEnd                           // 边界标记
)

// String 实现 String 方法
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

// ContactStatus 联系人状态
type ContactStatus int

const (
	ContactStatusPending  ContactStatus = iota + 1 // 待确认
	ContactStatusAccepted                          // 已接受
	ContactStatusBlocked                           // 已拉黑
	ContactStatusDeleted                           // 已删除
	ContactStatusEnd                               // 边界标记
)

// String 实现 String 方法
func (s ContactStatus) String() string {
	switch s {
	case ContactStatusPending:
		return "pending"
	case ContactStatusAccepted:
		return "accepted"
	case ContactStatusBlocked:
		return "blocked"
	case ContactStatusDeleted:
		return "deleted"
	default:
		return "unknown"
	}
}

// MessageStatus 消息状态
type MessageStatus int

const (
	MessageStatusSending   MessageStatus = iota + 1 // 发送中
	MessageStatusSent                               // 已发送
	MessageStatusDelivered                          // 已送达
	MessageStatusRead                               // 已读
	MessageStatusFailed                             // 发送失败
	MessageStatusEnd                                // 边界标记
)

// String 实现 String 方法
func (s MessageStatus) String() string {
	switch s {
	case MessageStatusSending:
		return "sending"
	case MessageStatusSent:
		return "sent"
	case MessageStatusDelivered:
		return "delivered"
	case MessageStatusRead:
		return "read"
	case MessageStatusFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// ConnectionStatus IM 连接状态
type ConnectionStatus int

const (
	ConnectionStatusOnline  ConnectionStatus = iota + 1 // 在线
	ConnectionStatusOffline                             // 离线
	ConnectionStatusAway                                // 离开
	ConnectionStatusBusy                                // 忙碌
	ConnectionStatusEnd                                 // 边界标记
)

// String 实现 String 方法
func (s ConnectionStatus) String() string {
	switch s {
	case ConnectionStatusOnline:
		return "online"
	case ConnectionStatusOffline:
		return "offline"
	case ConnectionStatusAway:
		return "away"
	case ConnectionStatusBusy:
		return "busy"
	default:
		return "unknown"
	}
}

// MessagingEvent 消息事件（用于 Kafka）
type MessagingEvent struct {
	Type      string      `json:"type"`      // 事件类型: message_sent, message_read, chat_created...
	SenderID  int64       `json:"sender_id"` // 发送者
	ChatID    int64       `json:"chat_id"`   // 会话ID
	Payload   interface{} `json:"payload"`   // 事件负载
	Timestamp int64       `json:"timestamp"` // 时间戳
}

// Messaging errors
var (
	ErrChatNotFound    = &MessagingError{Code: 50401, Message: "chat not found"}
	ErrMessageNotFound = &MessagingError{Code: 50402, Message: "message not found"}
	ErrContactNotFound = &MessagingError{Code: 50403, Message: "contact not found"}
	ErrNotInChat       = &MessagingError{Code: 50301, Message: "not in chat"}
	ErrAlreadyInChat   = &MessagingError{Code: 50302, Message: "already in chat"}
	ErrContactBlocked  = &MessagingError{Code: 50303, Message: "contact blocked"}
	ErrMessageTooLong  = &MessagingError{Code: 50404, Message: "message too long"}
)

// MessagingError Messaging 领域错误
type MessagingError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *MessagingError) Error() string {
	return e.Message
}
