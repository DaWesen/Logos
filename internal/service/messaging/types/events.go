package types

import (
	"encoding/json"
	"time"
)

type EventType string

const (
	EventTypeMessage         EventType = "message"
	EventTypeMessageRead     EventType = "message_read"
	EventTypeMessageWithdraw EventType = "message_withdraw"
	// EventTypeMessageDelete 单条消息「删除」（仅对删除者本人生效）。
	// 与撤回不同：删除不改写消息、不影响其他成员，只通知该用户的全部在线设备移除本地副本。
	EventTypeMessageDelete EventType = "message_delete"
	// EventTypeMessageEdit 消息编辑：广播给会话内所有成员（含发送者自己的其他设备）。
	// 各端按 version 幂等应用，只接受比本地更新的版本，避免乱序事件把新内容改回旧内容。
	EventTypeMessageEdit  EventType = "message_edit"
	EventTypeTyping       EventType = "typing"
	EventTypeUserOnline   EventType = "user_online"
	EventTypeUserOffline  EventType = "user_offline"
	EventTypeNotification EventType = "notification"
)

type MessageEvent struct {
	ID             string                 `json:"id"`
	EventType      EventType              `json:"event_type"`
	ChatID         string                 `json:"chat_id"`
	ChatType       ChatType               `json:"chat_type"`
	SenderID       string                 `json:"sender_id"`
	SenderName     string                 `json:"sender_name,omitempty"`
	SenderAvatar   string                 `json:"sender_avatar,omitempty"`
	MessageType    MessageType            `json:"message_type"`
	Content        string                 `json:"content"`
	MediaURL       string                 `json:"media_url,omitempty"`
	MediaMeta      json.RawMessage        `json:"media_meta,omitempty"`
	Metadata       map[string]string      `json:"metadata,omitempty"`
	Timestamp      time.Time              `json:"timestamp"`
	ReplyToMessage string                 `json:"reply_to_message,omitempty"`
	MentionUserIDs []string               `json:"mention_user_ids,omitempty"`
	Extra          map[string]interface{} `json:"extra,omitempty"`
	RecipientIDs   []string               `json:"recipient_ids,omitempty"`
	Channel        string                 `json:"channel,omitempty"`
	// RequestID 客户端发送幂等键（WS/TCP 帧上的 request_id）。
	// 网关按 (sender_id, request_id) 派生确定性消息 ID，服务端以它为幂等键落库，
	// 使同一发送请求经 WS、TCP 或 REST 任一路径重发都只会产生一条消息。
	RequestID string `json:"request_id,omitempty"`
}

type MessageReadEvent struct {
	EventType    EventType `json:"event_type"`
	MessageIDs   []string  `json:"message_ids"`
	ReaderID     string    `json:"reader_id"`
	ChatID       string    `json:"chat_id"`
	ChatType     ChatType  `json:"chat_type,omitempty"`
	RecipientIDs []string  `json:"recipient_ids,omitempty"`
	Timestamp    time.Time `json:"timestamp"`
}

type MessageWithdrawEvent struct {
	EventType EventType `json:"event_type"`
	MessageID string    `json:"message_id"`
	ChatID    string    `json:"chat_id"`
	ChatType  ChatType  `json:"chat_type"`
	SenderID  string    `json:"sender_id"`
	// Version 撤回后的版本号（撤回也会 version+1），客户端只在版本比本地新时应用，
	// 防止更早发出的编辑事件把已撤回的消息重新写回正文。
	Version      int64     `json:"version"`
	RecipientIDs []string  `json:"recipient_ids,omitempty"`
	Timestamp    time.Time `json:"timestamp"`
}

func (e *MessageWithdrawEvent) ToJSON() ([]byte, error) {
	return json.Marshal(e)
}

func MessageWithdrawEventFromJSON(data []byte) (*MessageWithdrawEvent, error) {
	var e MessageWithdrawEvent
	err := json.Unmarshal(data, &e)
	if err != nil {
		return nil, err
	}
	return &e, nil
}

// MessageDeleteEvent 单条消息删除事件。
// 只投递给删除者本人（RecipientIDs 恒为 [OwnerID]），由该用户的所有在线设备消费；
// 会话内其他成员收不到，避免暴露「某人删除了哪条消息」这一私有行为。
type MessageDeleteEvent struct {
	EventType    EventType `json:"event_type"`
	MessageID    string    `json:"message_id"`
	ChatID       string    `json:"chat_id"`
	ChatType     ChatType  `json:"chat_type"`
	OwnerID      string    `json:"owner_id"`
	RecipientIDs []string  `json:"recipient_ids,omitempty"`
	Timestamp    time.Time `json:"timestamp"`
}

func (e *MessageDeleteEvent) ToJSON() ([]byte, error) {
	return json.Marshal(e)
}

func MessageDeleteEventFromJSON(data []byte) (*MessageDeleteEvent, error) {
	var e MessageDeleteEvent
	if err := json.Unmarshal(data, &e); err != nil {
		return nil, err
	}
	return &e, nil
}

// MessageEditEvent 消息编辑事件。
//
// 与删除相反，编辑是**全局状态**：会话内所有成员看到的都应是最新内容，因此广播给全部成员；
// 发送者自己的其他在线设备同样要收到（getPrivateChatRecipient 只返回对方，需显式补上发送者）。
// 事件携带 version，接收端只在 version 比自己本地新时才应用，乱序/重复投递不会回退内容。
type MessageEditEvent struct {
	EventType    EventType `json:"event_type"`
	MessageID    string    `json:"message_id"`
	ChatID       string    `json:"chat_id"`
	ChatType     ChatType  `json:"chat_type"`
	SenderID     string    `json:"sender_id"`
	Content      string    `json:"content"`
	Version      int64     `json:"version"`
	RecipientIDs []string  `json:"recipient_ids,omitempty"`
	Timestamp    time.Time `json:"timestamp"`
}

func (e *MessageEditEvent) ToJSON() ([]byte, error) {
	return json.Marshal(e)
}

func MessageEditEventFromJSON(data []byte) (*MessageEditEvent, error) {
	var e MessageEditEvent
	if err := json.Unmarshal(data, &e); err != nil {
		return nil, err
	}
	return &e, nil
}

type TypingEvent struct {
	EventType    EventType `json:"event_type"`
	UserID       string    `json:"user_id"`
	ChatID       string    `json:"chat_id"`
	IsTyping     bool      `json:"is_typing"`
	RecipientIDs []string  `json:"recipient_ids,omitempty"`
	Timestamp    time.Time `json:"timestamp"`
}

func (e *TypingEvent) ToJSON() ([]byte, error) {
	return json.Marshal(e)
}

func TypingEventFromJSON(data []byte) (*TypingEvent, error) {
	var e TypingEvent
	err := json.Unmarshal(data, &e)
	if err != nil {
		return nil, err
	}
	return &e, nil
}

func DetectEventType(data []byte) EventType {
	var partial struct {
		EventType EventType `json:"event_type"`
	}
	if err := json.Unmarshal(data, &partial); err != nil {
		return ""
	}
	return partial.EventType
}

type UserPresenceEvent struct {
	UserID    string    `json:"user_id"`
	DeviceID  string    `json:"device_id,omitempty"`
	Online    bool      `json:"online"`
	Timestamp time.Time `json:"timestamp"`
}

type NotificationEvent struct {
	ID        string            `json:"id"`
	UserID    string            `json:"user_id"`
	Type      string            `json:"type"`
	Title     string            `json:"title"`
	Content   string            `json:"content"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	Timestamp time.Time         `json:"timestamp"`
}

func NewMessageEvent(
	id, chatID string,
	chatType ChatType,
	senderID string,
	messageType MessageType,
	content string,
	metadata map[string]string,
	replyTo string,
	mentionUserIDs []string,
	recipientIDs []string,
) *MessageEvent {
	return &MessageEvent{
		EventType:      EventTypeMessage,
		ID:             id,
		ChatID:         chatID,
		ChatType:       chatType,
		SenderID:       senderID,
		MessageType:    messageType,
		Content:        content,
		Metadata:       metadata,
		Timestamp:      time.Now(),
		ReplyToMessage: replyTo,
		MentionUserIDs: mentionUserIDs,
		RecipientIDs:   recipientIDs,
	}
}

func NewMediaMessageEvent(
	id, chatID string,
	chatType ChatType,
	senderID string,
	messageType MessageType,
	content string,
	mediaURL string,
	mediaMeta json.RawMessage,
	metadata map[string]string,
	replyTo string,
	mentionUserIDs []string,
	recipientIDs []string,
) *MessageEvent {
	return &MessageEvent{
		EventType:      EventTypeMessage,
		ID:             id,
		ChatID:         chatID,
		ChatType:       chatType,
		SenderID:       senderID,
		MessageType:    messageType,
		Content:        content,
		MediaURL:       mediaURL,
		MediaMeta:      mediaMeta,
		Metadata:       metadata,
		Timestamp:      time.Now(),
		ReplyToMessage: replyTo,
		MentionUserIDs: mentionUserIDs,
		RecipientIDs:   recipientIDs,
	}
}

func (e *MessageEvent) ToJSON() ([]byte, error) {
	return json.Marshal(e)
}

func MessageEventFromJSON(data []byte) (*MessageEvent, error) {
	var e MessageEvent
	err := json.Unmarshal(data, &e)
	if err != nil {
		return nil, err
	}
	return &e, nil
}

func (e *UserPresenceEvent) ToJSON() ([]byte, error) {
	return json.Marshal(e)
}

func UserPresenceEventFromJSON(data []byte) (*UserPresenceEvent, error) {
	var e UserPresenceEvent
	err := json.Unmarshal(data, &e)
	if err != nil {
		return nil, err
	}
	return &e, nil
}

func (e *NotificationEvent) ToJSON() ([]byte, error) {
	return json.Marshal(e)
}

func NotificationEventFromJSON(data []byte) (*NotificationEvent, error) {
	var e NotificationEvent
	err := json.Unmarshal(data, &e)
	if err != nil {
		return nil, err
	}
	return &e, nil
}

func (e *MessageReadEvent) ToJSON() ([]byte, error) {
	return json.Marshal(e)
}

func MessageReadEventFromJSON(data []byte) (*MessageReadEvent, error) {
	var e MessageReadEvent
	err := json.Unmarshal(data, &e)
	if err != nil {
		return nil, err
	}
	return &e, nil
}
