package websocket

// MessageType 表示 WebSocket 消息类型
type MessageType string

const (
	// MessageTypeConnect 连接消息
	MessageTypeConnect MessageType = "connect"
	// MessageTypeDisconnect 断开连接消息
	MessageTypeDisconnect MessageType = "disconnect"
	// MessageTypeHeartbeat 心跳消息
	MessageTypeHeartbeat MessageType = "heartbeat"
	// MessageTypeMessage 聊天消息
	MessageTypeMessage MessageType = "message"
	// MessageTypeTyping 输入状态消息
	MessageTypeTyping MessageType = "typing"
	// MessageTypeReadReceipt 已读回执
	MessageTypeReadReceipt MessageType = "read_receipt"
	// MessageTypeWithdraw 消息撤回
	MessageTypeWithdraw MessageType = "withdraw"
	// MessageTypeOnlineStatus 在线状态变更
	MessageTypeOnlineStatus MessageType = "online_status"
	// MessageTypeError 错误消息
	MessageTypeError MessageType = "error"
	// MessageTypeAck 确认消息
	MessageTypeAck MessageType = "ack"
)

// IncomingMessage 表示从客户端接收的消息
type IncomingMessage struct {
	Type      MessageType `json:"type"`
	RequestID string      `json:"request_id,omitempty"`
	Payload   interface{} `json:"data"`
}

// OutgoingMessage 表示发送到客户端的消息
type OutgoingMessage struct {
	Type      MessageType `json:"type"`
	RequestID string      `json:"request_id,omitempty"`
	Payload   interface{} `json:"data"`
	Timestamp int64       `json:"timestamp"`
}

// ConnectPayload 连接载荷
type ConnectPayload struct {
	Token    string `json:"token"`
	DeviceID string `json:"device_id"`
}

type TypingPayload struct {
	ChatID string `json:"chat_id"`
	Typing bool   `json:"typing"`
}

// ReadReceiptPayload 已读回执载荷
type ReadReceiptPayload struct {
	ChatID    string `json:"chat_id"`
	MessageID string `json:"message_id"`
}

// MessagePayload 聊天消息载荷
type MessagePayload struct {
	ChatID         string                 `json:"chat_id"`
	Content        string                 `json:"content"`
	ChatType       int32                  `json:"chat_type"`
	MessageType    int32                  `json:"message_type"`
	MentionUserIDs []string               `json:"mention_user_ids,omitempty"`
	Extra          map[string]interface{} `json:"extra,omitempty"`
}

// ErrorPayload 错误载荷
type ErrorPayload struct {
	Code    int32  `json:"code"`
	Message string `json:"message"`
}

// AckPayload 确认载荷
type AckPayload struct {
	MessageID string `json:"message_id"`
}

// ConnectResponsePayload 连接响应载荷
type ConnectResponsePayload struct {
	SessionID string `json:"session_id"`
}
