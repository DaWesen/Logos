package tcp

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"strings"
	"sync"
	"time"

	"Logos/internal/service/messaging/types"
	"Logos/internal/service/platform/gateway"
	"Logos/pkg/jwt"
	"Logos/pkg/logger"
	"Logos/pkg/mq"

	"github.com/google/uuid"
)

type MessageType string

const (
	MessageTypeConnect     MessageType = "connect"
	MessageTypeDisconnect  MessageType = "disconnect"
	MessageTypeHeartbeat   MessageType = "heartbeat"
	MessageTypeMessage     MessageType = "message"
	MessageTypeTyping      MessageType = "typing"
	MessageTypeReadReceipt MessageType = "read_receipt"
	MessageTypeError       MessageType = "error"
	MessageTypeAck         MessageType = "ack"
)

type IncomingMessage struct {
	Type      MessageType     `json:"type"`
	RequestID string          `json:"request_id"`
	Payload   json.RawMessage `json:"payload"`
}

type OutgoingMessage struct {
	Type      MessageType     `json:"type"`
	RequestID string          `json:"request_id"`
	Payload   json.RawMessage `json:"payload"`
	Timestamp int64           `json:"timestamp"`
}

type ConnectPayload struct {
	Token    string `json:"token"`
	DeviceID string `json:"device_id"`
}

type HeartbeatPayload struct {
	Timestamp int64 `json:"timestamp"`
}

type TypingPayload struct {
	ChatID   string `json:"chat_id"`
	ChatType int    `json:"chat_type"`
}

type ReadReceiptPayload struct {
	ChatID     string   `json:"chat_id"`
	MessageIDs []string `json:"message_ids"`
}

type MessagePayload struct {
	ChatID      string                 `json:"chat_id"`
	Content     string                 `json:"content"`
	ChatType    int                    `json:"chat_type"`
	MessageType int                    `json:"message_type"`
	ReplyTo     string                 `json:"reply_to,omitempty"`
	MentionIDs  []string               `json:"mention_user_ids,omitempty"`
	Extra       map[string]interface{} `json:"extra,omitempty"`
}

type ErrorPayload struct {
	Code    int32  `json:"code"`
	Message string `json:"message"`
}

type AckPayload struct {
	MessageID string `json:"message_id"`
}

type ConnectResponsePayload struct {
	SessionID string `json:"session_id"`
}

type TCPConnection struct {
	Conn      net.Conn
	UserID    string
	DeviceID  string
	SessionID string
	Send      chan []byte
	mu        sync.Mutex
	IsClosed  bool
}

type Handler struct {
	manager    *ConnectionManager
	unified    *gateway.UnifiedConnectionManager
	jwtManager *jwt.JWTManager
	eventBus   *types.EventBus
	ctx        context.Context
	cancel     context.CancelFunc
}

func NewHandler(mgr *ConnectionManager) *Handler {
	ctx, cancel := context.WithCancel(context.Background())
	h := &Handler{
		manager:    mgr,
		unified:    gateway.GetUnifiedConnectionManager(),
		jwtManager: jwt.NewJWTManager(),
		eventBus:   types.GetEventBus(),
		ctx:        ctx,
		cancel:     cancel,
	}
	h.startConsumers()
	return h
}

func (h *Handler) startConsumers() {
	go func() {
		if h.eventBus == nil {
			return
		}
		err := h.eventBus.SubscribeChatOutgoing(h.ctx, func(msg *mq.Message) error {
			h.handleChatMessage(msg)
			return nil
		}, "gateway-tcp-chat-outgoing-consumer")
		if err != nil {
			logger.Error("TCP: 订阅ChatOutgoing事件失败", logger.ErrorField(err))
		}
	}()

	go func() {
		if h.eventBus == nil {
			return
		}
		err := h.eventBus.SubscribeIMEvents(h.ctx, func(msg *mq.Message) error {
			h.handleIMMessage(msg)
			return nil
		}, "gateway-tcp-im-consumer")
		if err != nil {
			logger.Error("TCP: 订阅IM事件失败", logger.ErrorField(err))
		}
	}()
}

func (h *Handler) handleChatMessage(msg *mq.Message) {
	eventType := types.DetectEventType(msg.Value)

	switch eventType {
	case types.EventTypeMessage, "":
		event, err := types.MessageEventFromJSON(msg.Value)
		if err != nil {
			return
		}
		h.broadcastToRelevantUsers(event, msg.Value)
	case types.EventTypeMessageRead:
		event, err := types.MessageReadEventFromJSON(msg.Value)
		if err != nil {
			return
		}
		h.handleMessageReadEvent(event, msg.Value)
	case types.EventTypeTyping:
		event, err := types.TypingEventFromJSON(msg.Value)
		if err != nil {
			return
		}
		h.handleTypingEvent(event, msg.Value)
	}
}

func (h *Handler) handleMessageReadEvent(event *types.MessageReadEvent, data []byte) {
	if len(event.RecipientIDs) > 0 {
		h.unified.SendToUsers(event.RecipientIDs, data)
	}
}

func (h *Handler) handleTypingEvent(event *types.TypingEvent, data []byte) {
	if len(event.RecipientIDs) > 0 {
		h.unified.SendToUsers(event.RecipientIDs, data)
	}
}

func (h *Handler) handleIMMessage(msg *mq.Message) {
	h.unified.BroadcastMessage(msg.Value)
}

func (h *Handler) broadcastToRelevantUsers(event *types.MessageEvent, data []byte) {
	if len(event.RecipientIDs) > 0 {
		h.unified.SendToUsers(event.RecipientIDs, data)
		return
	}

	chatType := event.ChatType
	switch chatType {
	case types.ChatTypePrivate:
		parts := strings.SplitN(event.ChatID, "_", 3)
		if len(parts) == 3 {
			h.unified.SendToUser(parts[1], data)
			h.unified.SendToUser(parts[2], data)
		}
	case types.ChatTypeGroup, types.ChatTypeBroadcast:
		h.unified.BroadcastMessage(data)
	}
}

func (h *Handler) ServeTCP(conn net.Conn) {
	tcpConn := &TCPConnection{
		Conn:     conn,
		Send:     make(chan []byte, 256),
		IsClosed: false,
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go h.readPump(tcpConn, &wg)
	go h.writePump(tcpConn, &wg)
	wg.Wait()
}

func (h *Handler) readPump(conn *TCPConnection, wg *sync.WaitGroup) {
	defer func() {
		wg.Done()
		h.cleanup(conn)
	}()

	conn.Conn.SetReadDeadline(time.Now().Add(120 * time.Second))
	scanner := bufio.NewScanner(conn.Conn)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for {
		if !scanner.Scan() {
			break
		}
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		conn.Conn.SetReadDeadline(time.Now().Add(120 * time.Second))

		var msg IncomingMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			h.sendError(conn, "", 400, "消息格式无效")
			continue
		}

		h.handleMessage(conn, &msg)
	}
}

func (h *Handler) writePump(conn *TCPConnection, wg *sync.WaitGroup) {
	defer func() {
		wg.Done()
		conn.Conn.Close()
	}()

	for {
		select {
		case <-h.ctx.Done():
			return
		case data, ok := <-conn.Send:
			if !ok {
				return
			}
			conn.mu.Lock()
			err := conn.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err == nil {
				_, err = conn.Conn.Write(append(data, '\n'))
			}
			conn.mu.Unlock()
			if err != nil {
				return
			}
		}
	}
}

func (h *Handler) handleMessage(conn *TCPConnection, msg *IncomingMessage) {
	switch msg.Type {
	case MessageTypeConnect:
		h.handleConnect(conn, msg)
	case MessageTypeDisconnect:
		h.handleDisconnect(conn, msg)
	case MessageTypeHeartbeat:
		h.handleHeartbeat(conn, msg)
	case MessageTypeMessage:
		h.handleChatMessageSend(conn, msg)
	case MessageTypeTyping:
		h.handleTyping(conn, msg)
	case MessageTypeReadReceipt:
		h.handleReadReceipt(conn, msg)
	default:
		h.sendError(conn, msg.RequestID, 400, "未知消息类型")
	}
}

func (h *Handler) handleConnect(conn *TCPConnection, msg *IncomingMessage) {
	var payload ConnectPayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		h.sendError(conn, msg.RequestID, 400, "载荷格式无效")
		return
	}

	claims, err := h.jwtManager.ParseToken(payload.Token)
	if err != nil {
		h.sendError(conn, msg.RequestID, 401, "令牌无效")
		return
	}

	sessionID := uuid.New().String()
	conn.UserID = claims.UserID
	conn.DeviceID = payload.DeviceID
	conn.SessionID = sessionID

	h.manager.AddConnection(sessionID, conn)
	h.unified.Register(sessionID, conn.UserID, conn.DeviceID, "tcp", func(data []byte) {
		conn.mu.Lock()
		if conn.IsClosed {
			conn.mu.Unlock()
			return
		}
		conn.mu.Unlock()

		select {
		case conn.Send <- data:
		default:
			logger.Warn("TCP发送通道已满", logger.StringField("session_id", sessionID))
		}
	})

	if h.eventBus != nil {
		presenceEvent := &types.UserPresenceEvent{
			UserID:    conn.UserID,
			DeviceID:  conn.DeviceID,
			Online:    true,
			Timestamp: time.Now(),
		}
		if err := h.eventBus.PublishPresenceEvent(h.ctx, presenceEvent); err != nil {
			logger.Warn("TCP: 发布用户上线事件失败", logger.ErrorField(err))
		}
	}

	logger.Info("TCP 已连接",
		logger.StringField("user_id", conn.UserID),
		logger.StringField("session_id", sessionID))

	respPayload, _ := json.Marshal(ConnectResponsePayload{SessionID: sessionID})
	h.sendMessage(conn, OutgoingMessage{
		Type:      MessageTypeConnect,
		RequestID: msg.RequestID,
		Payload:   respPayload,
		Timestamp: time.Now().UnixMilli(),
	})
}

func (h *Handler) handleDisconnect(conn *TCPConnection, msg *IncomingMessage) {
	h.cleanup(conn)
	h.sendMessage(conn, OutgoingMessage{
		Type:      MessageTypeDisconnect,
		RequestID: msg.RequestID,
		Timestamp: time.Now().UnixMilli(),
	})
}

func (h *Handler) handleHeartbeat(conn *TCPConnection, msg *IncomingMessage) {
	respPayload, _ := json.Marshal(HeartbeatPayload{Timestamp: time.Now().UnixMilli()})
	h.sendMessage(conn, OutgoingMessage{
		Type:      MessageTypeHeartbeat,
		RequestID: msg.RequestID,
		Payload:   respPayload,
		Timestamp: time.Now().UnixMilli(),
	})
}

func (h *Handler) handleChatMessageSend(conn *TCPConnection, msg *IncomingMessage) {
	if conn.UserID == "" {
		h.sendError(conn, msg.RequestID, 401, "未连接")
		return
	}

	var payload MessagePayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		h.sendError(conn, msg.RequestID, 400, "载荷格式无效")
		return
	}

	if payload.ChatID == "" || payload.Content == "" {
		h.sendError(conn, msg.RequestID, 400, "会话ID和内容不能为空")
		return
	}

	if payload.ChatType == 0 {
		payload.ChatType = 1
	}
	if payload.MessageType == 0 {
		payload.MessageType = 1
	}

	var metadata map[string]string
	if payload.Extra != nil {
		metadata = make(map[string]string)
		for k, v := range payload.Extra {
			if str, ok := v.(string); ok {
				metadata[k] = str
			}
		}
	}

	messageID := uuid.New().String()
	event := types.NewMessageEvent(
		messageID,
		payload.ChatID,
		types.ChatType(payload.ChatType),
		conn.UserID,
		types.MessageType(payload.MessageType),
		payload.Content,
		metadata,
		payload.ReplyTo,
		payload.MentionIDs,
		nil,
	)

	if err := h.eventBus.PublishMessageEvent(h.ctx, event); err != nil {
		h.sendError(conn, msg.RequestID, 500, "发送消息失败")
		return
	}

	ackPayload, _ := json.Marshal(AckPayload{MessageID: messageID})
	h.sendMessage(conn, OutgoingMessage{
		Type:      MessageTypeAck,
		RequestID: msg.RequestID,
		Payload:   ackPayload,
		Timestamp: time.Now().UnixMilli(),
	})
}

func (h *Handler) handleTyping(conn *TCPConnection, msg *IncomingMessage) {
	if conn.UserID == "" {
		h.sendError(conn, msg.RequestID, 401, "未连接")
		return
	}

	var payload TypingPayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		h.sendError(conn, msg.RequestID, 400, "载荷格式无效")
		return
	}

	event := &types.TypingEvent{
		EventType: types.EventTypeTyping,
		UserID:    conn.UserID,
		ChatID:    payload.ChatID,
		Timestamp: time.Now(),
	}

	if err := h.eventBus.PublishTypingEvent(h.ctx, event); err != nil {
		h.sendError(conn, msg.RequestID, 500, "发送输入状态失败")
		return
	}

	ackPayload, _ := json.Marshal(AckPayload{})
	h.sendMessage(conn, OutgoingMessage{
		Type:      MessageTypeAck,
		RequestID: msg.RequestID,
		Payload:   ackPayload,
		Timestamp: time.Now().UnixMilli(),
	})
}

func (h *Handler) handleReadReceipt(conn *TCPConnection, msg *IncomingMessage) {
	if conn.UserID == "" {
		h.sendError(conn, msg.RequestID, 401, "未连接")
		return
	}

	var payload ReadReceiptPayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		h.sendError(conn, msg.RequestID, 400, "载荷格式无效")
		return
	}

	event := &types.MessageReadEvent{
		EventType:  types.EventTypeMessageRead,
		ReaderID:   conn.UserID,
		ChatID:     payload.ChatID,
		MessageIDs: payload.MessageIDs,
		Timestamp:  time.Now(),
	}

	if err := h.eventBus.PublishMessageReadEvent(h.ctx, event); err != nil {
		h.sendError(conn, msg.RequestID, 500, "发送已读回执失败")
		return
	}

	ackPayload, _ := json.Marshal(AckPayload{})
	h.sendMessage(conn, OutgoingMessage{
		Type:      MessageTypeAck,
		RequestID: msg.RequestID,
		Payload:   ackPayload,
		Timestamp: time.Now().UnixMilli(),
	})
}

func (h *Handler) sendMessage(conn *TCPConnection, msg OutgoingMessage) {
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	select {
	case conn.Send <- data:
	default:
		h.cleanup(conn)
	}
}

func (h *Handler) sendError(conn *TCPConnection, requestID string, code int32, message string) {
	errPayload, _ := json.Marshal(ErrorPayload{Code: code, Message: message})
	h.sendMessage(conn, OutgoingMessage{
		Type:      MessageTypeError,
		RequestID: requestID,
		Payload:   errPayload,
		Timestamp: time.Now().UnixMilli(),
	})
}

func (h *Handler) cleanup(conn *TCPConnection) {
	conn.mu.Lock()
	if conn.IsClosed {
		conn.mu.Unlock()
		return
	}
	conn.IsClosed = true
	close(conn.Send)
	conn.mu.Unlock()

	if conn.SessionID != "" {
		h.manager.RemoveConnection(conn.SessionID)
		h.unified.Unregister(conn.SessionID)
	}

	if conn.UserID != "" && h.eventBus != nil {
		presenceEvent := &types.UserPresenceEvent{
			UserID:    conn.UserID,
			DeviceID:  conn.DeviceID,
			Online:    false,
			Timestamp: time.Now(),
		}
		_ = h.eventBus.PublishPresenceEvent(h.ctx, presenceEvent)
	}

	logger.Info("TCP 连接关闭",
		logger.StringField("user_id", conn.UserID),
		logger.StringField("session_id", conn.SessionID))
}

func (h *Handler) Close() error {
	h.cancel()
	if h.eventBus != nil {
		_ = h.eventBus.Close()
	}
	return nil
}
