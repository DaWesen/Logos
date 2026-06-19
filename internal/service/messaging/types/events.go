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
	EventTypeTyping          EventType = "typing"
	EventTypeUserOnline      EventType = "user_online"
	EventTypeUserOffline     EventType = "user_offline"
	EventTypeNotification    EventType = "notification"
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
	EventType    EventType `json:"event_type"`
	MessageID    string    `json:"message_id"`
	ChatID       string    `json:"chat_id"`
	ChatType     ChatType  `json:"chat_type"`
	SenderID     string    `json:"sender_id"`
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
