package types

type MessageType int

const (
	MessageTypeText     MessageType = iota + 1
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
	ChatTypePrivate   ChatType = iota + 1
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
)

type MessagingError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *MessagingError) Error() string {
	return e.Message
}
