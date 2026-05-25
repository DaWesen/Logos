package websocket

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"Logos/internal/service/messaging/types"
	"Logos/internal/service/platform/gateway"
	"Logos/pkg/client"
	"Logos/pkg/jwt"
	"Logos/pkg/logger"
	"Logos/pkg/mq"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// Handler 处理 WebSocket 连接（更简化，专注于连接管理和转发）
type Handler struct {
	manager    *ConnectionManager
	unified    *gateway.UnifiedConnectionManager
	jwtManager *jwt.JWTManager
	eventBus   *types.EventBus
	userClient *client.UserClient
	ctx        context.Context
	cancel     context.CancelFunc
}

func NewHandler() *Handler {
	ctx, cancel := context.WithCancel(context.Background())
	h := &Handler{
		manager:    NewConnectionManager(),
		unified:    gateway.GetUnifiedConnectionManager(),
		jwtManager: jwt.NewJWTManager(),
		eventBus:   types.GetEventBus(),
		ctx:        ctx,
		cancel:     cancel,
	}
	h.startConsumers()
	return h
}

func (h *Handler) SetUserClient(uc *client.UserClient) {
	h.userClient = uc
}

// startConsumers 启动 Kafka 消费者
func (h *Handler) startConsumers() {
	go func() {
		chatHandler := func(msg *mq.Message) error {
			return h.handleChatEvent(msg)
		}
		if err := h.eventBus.SubscribeChatOutgoing(h.ctx, chatHandler); err != nil {
			logger.Error("订阅ChatOutgoing事件失败", logger.ErrorField(err))
		}
	}()

	go func() {
		imHandler := func(msg *mq.Message) error {
			return h.handleIMEvent(msg)
		}
		if err := h.eventBus.SubscribeIMEvents(h.ctx, imHandler); err != nil {
			logger.Error("订阅IM事件失败", logger.ErrorField(err))
		}
	}()

	go func() {
		notificationHandler := func(msg *mq.Message) error {
			return h.handleNotificationEvent(msg)
		}
		if err := h.eventBus.SubscribeNotifications(h.ctx, notificationHandler); err != nil {
			logger.Error("订阅通知事件失败", logger.ErrorField(err))
		}
	}()
}

// handleChatEvent 处理聊天事件（转发给接收者）
func (h *Handler) handleChatEvent(msg *mq.Message) error {
	logger.Info("收到出站聊天事件", logger.StringField("topic", msg.Topic))

	eventType := types.DetectEventType(msg.Value)

	switch eventType {
	case types.EventTypeTyping:
		typingEvent, err := types.TypingEventFromJSON(msg.Value)
		if err != nil {
			logger.Error("解析输入状态事件失败", logger.ErrorField(err))
			return err
		}
		if len(typingEvent.RecipientIDs) > 0 {
			return h.handleTypingEvent(typingEvent)
		}

	case types.EventTypeMessageRead:
		readEvent, err := types.MessageReadEventFromJSON(msg.Value)
		if err != nil {
			logger.Error("解析已读回执事件失败", logger.ErrorField(err))
			return err
		}
		if len(readEvent.RecipientIDs) > 0 {
			return h.handleMessageReadEvent(readEvent)
		}

	case types.EventTypeMessageWithdraw:
		withdrawEvent, err := types.MessageWithdrawEventFromJSON(msg.Value)
		if err != nil {
			logger.Error("解析撤回事件失败", logger.ErrorField(err))
			return err
		}
		return h.handleMessageWithdrawEvent(withdrawEvent)

	case types.EventTypeMessage, "":
		event, err := types.MessageEventFromJSON(msg.Value)
		if err != nil {
			logger.Error("解析消息事件失败", logger.ErrorField(err))
			return err
		}

		if len(event.RecipientIDs) == 0 {
			logger.Warn("出站消息事件缺少RecipientIDs，跳过",
				logger.StringField("msg_id", event.ID),
				logger.StringField("chat_id", event.ChatID))
			return nil
		}

		if h.userClient != nil && event.SenderID != "" && event.SenderID != "system" && !strings.HasPrefix(event.SenderID, "bot_") {
			if uid, parseErr := strconv.ParseInt(event.SenderID, 10, 64); parseErr == nil {
				userInfo, userErr := h.userClient.GetUserInfo(context.Background(), uid)
				if userErr == nil {
					if event.SenderName == "" {
						event.SenderName = userInfo.Username
					}
					if event.SenderAvatar == "" && userInfo.Avatar != "" {
						event.SenderAvatar = userInfo.Avatar
					}
				}
			}
		}

		if event.ChatType == types.ChatTypePrivate {
			logger.Info("\x1b[36m🔴 5️⃣ [Kafka-Gateway] 收到 outgoing 私聊消息\x1b[0m",
				logger.StringField("msg_id", event.ID),
				logger.StringField("time", time.Now().Format("2006-01-02 15:04:05.000000")))
		}

		eventData := OutgoingMessage{
			Type:      MessageTypeMessage,
			Payload:   event,
			Timestamp: time.Now().UnixMilli(),
		}

		data, err := json.Marshal(eventData)
		if err != nil {
			logger.Error("序列化事件失败", logger.ErrorField(err))
			return err
		}

		// 🔴 6️⃣ 推送到前端 WebSocket
		if event.ChatType == types.ChatTypePrivate {
			logger.Info("\x1b[31m🔴 6️⃣ [Gateway-前端] 推送私聊消息到 WebSocket\x1b[0m",
				logger.StringField("msg_id", event.ID),
				logger.StringField("recipient_ids", strings.Join(event.RecipientIDs, ",")),
				logger.StringField("time", time.Now().Format("2006-01-02 15:04:05.000000")))
		}

		h.broadcastToRelevantUsers(event, data)
	}

	return nil
}

// handleMessageWithdrawEvent 处理消息撤回事件
func (h *Handler) handleMessageWithdrawEvent(event *types.MessageWithdrawEvent) error {
	logger.Info("收到消息撤回事件",
		logger.StringField("chat_id", event.ChatID),
		logger.StringField("message_id", event.MessageID),
		logger.StringField("sender_id", event.SenderID),
		logger.IntField("recipient_count", len(event.RecipientIDs)))

	eventData := OutgoingMessage{
		Type: MessageTypeWithdraw,
		Payload: map[string]interface{}{
			"message_id": event.MessageID,
			"chat_id":    event.ChatID,
			"chat_type":  event.ChatType,
			"sender_id":  event.SenderID,
			"timestamp":  event.Timestamp.UnixMilli(),
		},
		Timestamp: time.Now().UnixMilli(),
	}

	data, err := json.Marshal(eventData)
	if err != nil {
		logger.Error("序列化撤回事件失败", logger.ErrorField(err))
		return err
	}

	if len(event.RecipientIDs) > 0 {
		h.unified.SendToUsers(event.RecipientIDs, data)
	} else {
		h.unified.BroadcastMessageExcept(data, event.SenderID)
	}

	logger.Info("撤回通知已转发",
		logger.StringField("message_id", event.MessageID),
		logger.IntField("recipient_count", len(event.RecipientIDs)))

	return nil
}

// handleMessageReadEvent 处理消息已读事件
func (h *Handler) handleMessageReadEvent(event *types.MessageReadEvent) error {
	logger.Info("收到消息已读事件",
		logger.StringField("chat_id", event.ChatID),
		logger.StringField("reader_id", event.ReaderID),
		logger.IntField("message_count", len(event.MessageIDs)),
		logger.IntField("recipient_count", len(event.RecipientIDs)))

	eventData := OutgoingMessage{
		Type: MessageTypeReadReceipt,
		Payload: map[string]interface{}{
			"reader_id":   event.ReaderID,
			"chat_id":     event.ChatID,
			"message_ids": event.MessageIDs,
			"timestamp":   event.Timestamp.UnixMilli(),
		},
		Timestamp: time.Now().UnixMilli(),
	}

	data, err := json.Marshal(eventData)
	if err != nil {
		logger.Error("序列化已读事件失败", logger.ErrorField(err))
		return err
	}

	if len(event.RecipientIDs) > 0 {
		logger.Debug("使用 RecipientIDs 进行精确转发",
			logger.IntField("count", len(event.RecipientIDs)))
		h.unified.SendToUsers(event.RecipientIDs, data)
	} else {
		parts := strings.Split(event.ChatID, "_")
		if len(parts) == 2 {
			h.forwardReadReceiptForPrivateChat(event, data)
		} else {
			logger.Debug("群聊/其他类型会话，使用广播转发已读回执",
				logger.StringField("chat_id", event.ChatID))
			h.unified.BroadcastMessageExcept(data, event.ReaderID)
		}
	}

	logger.Info("已读回执已转发",
		logger.StringField("reader_id", event.ReaderID),
		logger.StringField("chat_id", event.ChatID))

	return nil
}

// handleTypingEvent 处理输入状态事件
func (h *Handler) handleTypingEvent(event *types.TypingEvent) error {
	logger.Info("收到输入状态事件",
		logger.StringField("chat_id", event.ChatID),
		logger.StringField("user_id", event.UserID),
		logger.BoolField("typing", event.IsTyping),
		logger.IntField("recipient_count", len(event.RecipientIDs)))

	eventData := OutgoingMessage{
		Type: MessageTypeTyping,
		Payload: map[string]interface{}{
			"user_id": event.UserID,
			"chat_id": event.ChatID,
			"typing":  event.IsTyping,
		},
		Timestamp: time.Now().UnixMilli(),
	}

	data, err := json.Marshal(eventData)
	if err != nil {
		logger.Error("序列化输入状态事件失败", logger.ErrorField(err))
		return err
	}

	if len(event.RecipientIDs) > 0 {
		logger.Debug("使用 RecipientIDs 进行精确转发",
			logger.IntField("count", len(event.RecipientIDs)))
		h.unified.SendToUsers(event.RecipientIDs, data)
	} else {
		parts := strings.Split(event.ChatID, "_")
		if len(parts) == 2 {
			h.forwardTypingForPrivateChat(event, data)
		} else {
			logger.Debug("群聊/其他类型会话，使用广播转发输入状态",
				logger.StringField("chat_id", event.ChatID))
			h.unified.BroadcastMessageExcept(data, event.UserID)
		}
	}

	logger.Info("输入状态已转发",
		logger.StringField("user_id", event.UserID),
		logger.StringField("chat_id", event.ChatID))

	return nil
}

// forwardTypingForPrivateChat 处理单聊输入状态的精确转发
func (h *Handler) forwardTypingForPrivateChat(event *types.TypingEvent, data []byte) {
	parts := strings.Split(event.ChatID, "_")
	if len(parts) != 2 {
		logger.Warn("单聊 ChatID 格式错误", logger.StringField("chat_id", event.ChatID))
		h.unified.BroadcastMessageExcept(data, event.UserID)
		return
	}

	user1 := parts[0]
	user2 := parts[1]

	var otherUser string
	switch event.UserID {
	case user1:
		otherUser = user2
	case user2:
		otherUser = user1
	default:
		logger.Warn("输入用户不在单聊会话中",
			logger.StringField("user_id", event.UserID),
			logger.StringField("chat_id", event.ChatID))
		return
	}

	h.unified.SendToUser(otherUser, data)
	logger.Debug("输入状态已精确转发给单聊对方",
		logger.StringField("user_id", event.UserID),
		logger.StringField("recipient_id", otherUser),
		logger.StringField("chat_id", event.ChatID))
}

// forwardReadReceiptForPrivateChat 处理单聊已读回执的精确转发
func (h *Handler) forwardReadReceiptForPrivateChat(event *types.MessageReadEvent, data []byte) {
	parts := strings.Split(event.ChatID, "_")
	if len(parts) != 2 {
		logger.Warn("单聊 ChatID 格式错误", logger.StringField("chat_id", event.ChatID))
		h.unified.BroadcastMessageExcept(data, event.ReaderID)
		return
	}

	user1 := parts[0]
	user2 := parts[1]

	var otherUser string
	switch event.ReaderID {
	case user1:
		otherUser = user2
	case user2:
		otherUser = user1
	default:
		logger.Warn("阅读者不在单聊会话中",
			logger.StringField("reader_id", event.ReaderID),
			logger.StringField("chat_id", event.ChatID))
		return
	}

	h.unified.SendToUser(otherUser, data)
	logger.Debug("已读回执已精确转发给单聊对方",
		logger.StringField("reader_id", event.ReaderID),
		logger.StringField("recipient_id", otherUser),
		logger.StringField("chat_id", event.ChatID))
}

// handleIMEvent 处理IM事件
func (h *Handler) handleIMEvent(msg *mq.Message) error {
	logger.Info("收到IM事件", logger.StringField("topic", msg.Topic))
	event, err := types.UserPresenceEventFromJSON(msg.Value)
	if err != nil {
		logger.Error("解析用户在线事件失败", logger.ErrorField(err))
		return err
	}

	eventData := OutgoingMessage{
		Type:      MessageTypeOnlineStatus,
		Payload:   event,
		Timestamp: time.Now().UnixMilli(),
	}

	data, err := json.Marshal(eventData)
	if err != nil {
		logger.Error("序列化事件失败", logger.ErrorField(err))
		return err
	}

	if event.UserID != "" {
		h.unified.BroadcastMessage(data)
	}
	return nil
}

// handleNotificationEvent 处理通知事件
func (h *Handler) handleNotificationEvent(msg *mq.Message) error {
	logger.Info("收到通知事件", logger.StringField("topic", msg.Topic))
	event, err := types.NotificationEventFromJSON(msg.Value)
	if err != nil {
		logger.Error("解析通知事件失败", logger.ErrorField(err))
		return err
	}

	eventData := OutgoingMessage{
		Type:      MessageTypeMessage,
		Payload:   event,
		Timestamp: time.Now().UnixMilli(),
	}

	data, err := json.Marshal(eventData)
	if err != nil {
		logger.Error("序列化事件失败", logger.ErrorField(err))
		return err
	}

	if event.UserID != "" {
		h.unified.SendToUser(event.UserID, data)
	}
	return nil
}

// broadcastToRelevantUsers 根据事件中的接收者列表转发消息
func (h *Handler) broadcastToRelevantUsers(event *types.MessageEvent, data []byte) {
	if len(event.RecipientIDs) > 0 {
		h.unified.SendToUsers(event.RecipientIDs, data)
		logger.Debug("message broadcast completed",
			logger.StringField("chat_id", event.ChatID),
			logger.IntField("recipient_count", len(event.RecipientIDs)))
		return
	}

	logger.Warn("no recipient_ids in event, fallback to broadcast",
		logger.StringField("chat_id", event.ChatID))
	h.unified.BroadcastMessageExcept(data, event.SenderID)
}

// HandleWebSocket 处理 WebSocket 升级和连接
func (h *Handler) HandleWebSocket(c *gin.Context) {
	queryToken := c.Query("token")

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		logger.Error("WebSocket 升级失败", logger.ErrorField(err))
		return
	}

	wsConn := &Connection{
		Conn:     conn,
		Send:     make(chan []byte, 256),
		IsClosed: false,
	}

	if queryToken != "" {
		claims, err := h.jwtManager.ParseToken(queryToken)
		if err != nil {
			logger.Warn("WebSocket query token 认证失败", logger.ErrorField(err))
			h.sendError(wsConn, "", 401, "令牌无效")
			wsConn.Conn.WriteMessage(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "authentication failed"))
			wsConn.Conn.Close()
			return
		}

		sessionID := uuid.New().String()
		wsConn.UserID = claims.UserID
		wsConn.SessionID = sessionID
		h.manager.AddConnection(sessionID, wsConn)
		h.unified.Register(sessionID, claims.UserID, "web", "websocket", func(data []byte) {
			wsConn.mu.Lock()
			if wsConn.IsClosed {
				wsConn.mu.Unlock()
				return
			}
			wsConn.mu.Unlock()

			select {
			case wsConn.Send <- data:
			default:
				logger.Warn("WebSocket发送通道已满", logger.StringField("session_id", sessionID))
			}
		})

		if h.eventBus != nil {
			presenceEvent := &types.UserPresenceEvent{
				UserID:    claims.UserID,
				DeviceID:  "web",
				Online:    true,
				Timestamp: time.Now(),
			}
			if err := h.eventBus.PublishPresenceEvent(h.ctx, presenceEvent); err != nil {
				logger.Warn("发布用户上线事件失败", logger.ErrorField(err))
			}
		}

		response := OutgoingMessage{
			Type: MessageTypeConnect,
			Payload: ConnectResponsePayload{
				SessionID: sessionID,
			},
			Timestamp: time.Now().UnixMilli(),
		}
		h.sendMessage(wsConn, response)

		logger.Info("WebSocket 已通过 query token 连接",
			logger.StringField("user_id", wsConn.UserID),
			logger.StringField("session_id", sessionID))
	}

	var wg sync.WaitGroup
	wg.Add(2)

	go h.readPump(wsConn, &wg)
	go h.writePump(wsConn, &wg)

	wg.Wait()
}

// readPump 从 WebSocket 连接读取消息
func (h *Handler) readPump(conn *Connection, wg *sync.WaitGroup) {
	defer func() {
		wg.Done()
		h.cleanup(conn)
	}()

	if conn.UserID == "" {
		conn.Conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	} else {
		conn.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	}
	conn.Conn.SetPongHandler(func(string) error {
		conn.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		_, msg, err := conn.Conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				logger.Error("WebSocket 读取错误", logger.ErrorField(err))
			}
			break
		}

		h.handleMessage(conn, msg)

		if conn.UserID != "" {
			conn.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		}
	}
}

// writePump 向 WebSocket 连接写入消息
func (h *Handler) writePump(conn *Connection, wg *sync.WaitGroup) {
	defer func() {
		wg.Done()
		h.cleanup(conn)
	}()

	ticker := time.NewTicker(60 * time.Second) // 减少 ping 频率，避免 too_many_pings
	defer ticker.Stop()

	for {
		select {
		case msg, ok := <-conn.Send:
			conn.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				conn.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			w, err := conn.Conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			w.Write(msg)

			if err := w.Close(); err != nil {
				return
			}

		case <-ticker.C:
			conn.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// handleMessage 处理接收的 WebSocket 消息
func (h *Handler) handleMessage(conn *Connection, rawMsg []byte) {
	var msg IncomingMessage
	if err := json.Unmarshal(rawMsg, &msg); err != nil {
		logger.Warn("解析 WebSocket 消息失败", logger.ErrorField(err))
		h.sendError(conn, msg.RequestID, 400, "消息格式无效")
		return
	}

	if conn.UserID == "" && msg.Type != MessageTypeConnect {
		h.sendError(conn, msg.RequestID, 401, "请先认证")
		return
	}

	switch msg.Type {
	case MessageTypeConnect:
		h.handleConnect(conn, &msg)
	case MessageTypeDisconnect:
		h.handleDisconnect(conn, &msg)
	case MessageTypeHeartbeat:
		h.handleHeartbeat(conn, &msg)
	case MessageTypeMessage:
		h.handleChatMessage(conn, &msg)
	case MessageTypeTyping:
		h.handleTyping(conn, &msg)
	case MessageTypeReadReceipt:
		h.handleReadReceipt(conn, &msg)
	default:
		h.sendError(conn, msg.RequestID, 400, "未知消息类型")
	}
}

// handleConnect 处理连接初始化
func (h *Handler) handleConnect(conn *Connection, msg *IncomingMessage) {
	if conn.UserID != "" {
		h.sendError(conn, msg.RequestID, 400, "已经认证")
		return
	}

	payloadBytes, err := json.Marshal(msg.Payload)
	if err != nil {
		h.sendError(conn, msg.RequestID, 400, "载荷无效")
		return
	}

	var payload ConnectPayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		h.sendError(conn, msg.RequestID, 400, "载荷格式无效")
		return
	}

	if payload.Token == "" {
		h.sendError(conn, msg.RequestID, 401, "缺少令牌")
		conn.Conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "authentication required"))
		conn.Conn.Close()
		return
	}

	claims, err := h.jwtManager.ParseToken(payload.Token)
	if err != nil {
		h.sendError(conn, msg.RequestID, 401, "令牌无效")
		conn.Conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "authentication failed"))
		conn.Conn.Close()
		return
	}

	sessionID := uuid.New().String()
	conn.UserID = claims.UserID
	conn.DeviceID = payload.DeviceID
	conn.SessionID = sessionID

	h.manager.AddConnection(sessionID, conn)
	h.unified.Register(sessionID, claims.UserID, payload.DeviceID, "websocket", func(data []byte) {
		select {
		case conn.Send <- data:
		default:
			logger.Warn("WebSocket发送通道已满", logger.StringField("session_id", sessionID))
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
			logger.Warn("发布用户上线事件失败", logger.ErrorField(err))
		}
	}

	logger.Info("WebSocket 已连接",
		logger.StringField("user_id", conn.UserID),
		logger.StringField("device_id", conn.DeviceID),
		logger.StringField("session_id", sessionID))

	response := OutgoingMessage{
		Type:      MessageTypeConnect,
		RequestID: msg.RequestID,
		Payload: ConnectResponsePayload{
			SessionID: sessionID,
		},
		Timestamp: time.Now().UnixMilli(),
	}

	h.sendMessage(conn, response)
}

// handleDisconnect 处理断开连接
func (h *Handler) handleDisconnect(conn *Connection, msg *IncomingMessage) {
	if conn.SessionID != "" {
		h.manager.RemoveConnection(conn.SessionID)
		h.unified.Unregister(conn.SessionID)
	}

	response := OutgoingMessage{
		Type:      MessageTypeDisconnect,
		RequestID: msg.RequestID,
		Payload:   map[string]interface{}{},
		Timestamp: time.Now().UnixMilli(),
	}

	h.sendMessage(conn, response)
	conn.Conn.Close()
}

// handleHeartbeat 处理心跳
func (h *Handler) handleHeartbeat(conn *Connection, msg *IncomingMessage) {
	response := OutgoingMessage{
		Type:      MessageTypeHeartbeat,
		RequestID: msg.RequestID,
		Payload:   map[string]interface{}{},
		Timestamp: time.Now().UnixMilli(),
	}

	h.sendMessage(conn, response)
}

// handleChatMessage 处理聊天消息（发送到事件总线）
func (h *Handler) handleChatMessage(conn *Connection, msg *IncomingMessage) {
	if conn.UserID == "" {
		h.sendError(conn, msg.RequestID, 401, "未连接")
		return
	}

	// 解析 payload
	payloadBytes, err := json.Marshal(msg.Payload)
	if err != nil {
		h.sendError(conn, msg.RequestID, 400, "载荷无效")
		return
	}

	var messagePayload MessagePayload
	if err := json.Unmarshal(payloadBytes, &messagePayload); err != nil {
		h.sendError(conn, msg.RequestID, 400, "载荷格式无效")
		return
	}

	if messagePayload.ChatID == "" {
		h.sendError(conn, msg.RequestID, 400, "会话ID不能为空")
		return
	}
	if messagePayload.Content == "" {
		h.sendError(conn, msg.RequestID, 400, "消息内容不能为空")
		return
	}

	// 默认值处理
	if messagePayload.ChatType == 0 {
		messagePayload.ChatType = 1 // 默认为私聊
	}
	if messagePayload.MessageType == 0 {
		messagePayload.MessageType = 1 // 默认为文本
	}

	// 处理 metadata
	var metadata map[string]string
	if messagePayload.Extra != nil {
		metadata = make(map[string]string)
		for k, v := range messagePayload.Extra {
			if str, ok := v.(string); ok {
				metadata[k] = str
			}
		}
	}

	// 生成消息ID
	messageID := uuid.New().String()

	// 🔴 1️⃣ 前端发送消息链路开始
	if messagePayload.ChatType == 1 {
		logger.Info("\x1b[33m🔴 1️⃣ [前端-Gateway] 收到私聊消息\x1b[0m",
			logger.StringField("msg_id", messageID),
			logger.StringField("sender_id", conn.UserID),
			logger.StringField("chat_id", messagePayload.ChatID),
			logger.StringField("content", messagePayload.Content),
			logger.StringField("time", time.Now().Format("2006-01-02 15:04:05.000000")))
	}

	// 创建消息事件并发布（接收者留空，由 Chat 服务填充）
	event := types.NewMessageEvent(
		messageID,
		messagePayload.ChatID,
		types.ChatType(messagePayload.ChatType),
		conn.UserID,
		types.MessageType(messagePayload.MessageType),
		messagePayload.Content,
		metadata,
		"",
		messagePayload.MentionUserIDs,
		[]string{},
	)
	if len(messagePayload.Extra) > 0 {
		event.Extra = messagePayload.Extra
	}

	// 🔴 2️⃣ 发布到 Kafka
	if messagePayload.ChatType == 1 {
		logger.Info("\x1b[32m🔴 2️⃣ [Gateway-Kafka] 发布私聊消息到 Kafka\x1b[0m",
			logger.StringField("msg_id", messageID),
			logger.StringField("time", time.Now().Format("2006-01-02 15:04:05.000000")))
	}

	// 发布到事件总线
	if err := h.eventBus.PublishMessageEvent(h.ctx, event); err != nil {
		logger.Error("发布消息事件失败", logger.ErrorField(err))
		h.sendError(conn, msg.RequestID, 500, "发送消息失败")
		return
	}

	// 发送确认消息给发送者
	response := OutgoingMessage{
		Type:      MessageTypeAck,
		RequestID: msg.RequestID,
		Payload: AckPayload{
			MessageID: messageID,
		},
		Timestamp: time.Now().UnixMilli(),
	}

	h.sendMessage(conn, response)

	logger.Info("发送聊天消息成功",
		logger.StringField("message_id", messageID),
		logger.StringField("sender_id", conn.UserID),
		logger.StringField("chat_id", messagePayload.ChatID))
}

// handleTyping 处理输入状态
func (h *Handler) handleTyping(conn *Connection, msg *IncomingMessage) {
	if conn.UserID == "" {
		h.sendError(conn, msg.RequestID, 401, "未连接")
		return
	}

	// 解析 payload
	payloadBytes, err := json.Marshal(msg.Payload)
	if err != nil {
		h.sendError(conn, msg.RequestID, 400, "载荷无效")
		return
	}

	var typingPayload TypingPayload
	if err := json.Unmarshal(payloadBytes, &typingPayload); err != nil {
		h.sendError(conn, msg.RequestID, 400, "载荷格式无效")
		return
	}

	if typingPayload.ChatID == "" {
		h.sendError(conn, msg.RequestID, 400, "会话ID不能为空")
		return
	}

	// 构建输入状态事件并发布到 Kafka
	typingEvent := &types.TypingEvent{
		EventType: types.EventTypeTyping,
		UserID:    conn.UserID,
		ChatID:    typingPayload.ChatID,
		IsTyping:  typingPayload.Typing,
		Timestamp: time.Now(),
	}

	if err := h.eventBus.PublishTypingEvent(h.ctx, typingEvent); err != nil {
		logger.Error("发布输入状态事件失败", logger.ErrorField(err))
		h.sendError(conn, msg.RequestID, 500, "发送输入状态失败")
		return
	}

	// 发送确认
	ack := OutgoingMessage{
		Type:      MessageTypeAck,
		RequestID: msg.RequestID,
		Payload:   map[string]interface{}{"status": "success"},
		Timestamp: time.Now().UnixMilli(),
	}
	h.sendMessage(conn, ack)

	logger.Info("输入状态事件已发布",
		logger.StringField("user_id", conn.UserID),
		logger.StringField("chat_id", typingPayload.ChatID),
		logger.BoolField("typing", typingPayload.Typing))
}

// handleReadReceipt 处理已读回执
func (h *Handler) handleReadReceipt(conn *Connection, msg *IncomingMessage) {
	if conn.UserID == "" {
		h.sendError(conn, msg.RequestID, 401, "未连接")
		return
	}

	// 解析 payload
	payloadBytes, err := json.Marshal(msg.Payload)
	if err != nil {
		h.sendError(conn, msg.RequestID, 400, "载荷无效")
		return
	}

	var readPayload ReadReceiptPayload
	if err := json.Unmarshal(payloadBytes, &readPayload); err != nil {
		h.sendError(conn, msg.RequestID, 400, "载荷格式无效")
		return
	}

	if readPayload.ChatID == "" {
		h.sendError(conn, msg.RequestID, 400, "会话ID不能为空")
		return
	}
	if readPayload.MessageID == "" {
		h.sendError(conn, msg.RequestID, 400, "消息ID不能为空")
		return
	}

	// 创建 MessageReadEvent 并发布到 Kafka
	readEvent := &types.MessageReadEvent{
		EventType:  types.EventTypeMessageRead,
		MessageIDs: []string{readPayload.MessageID},
		ReaderID:   conn.UserID,
		ChatID:     readPayload.ChatID,
		Timestamp:  time.Now(),
	}

	// 发布到事件总线
	if err := h.eventBus.PublishMessageReadEvent(h.ctx, readEvent); err != nil {
		logger.Error("发布已读回执事件失败", logger.ErrorField(err))
		h.sendError(conn, msg.RequestID, 500, "发送已读回执失败")
		return
	}

	// 发送确认消息给发送者
	ack := OutgoingMessage{
		Type:      MessageTypeAck,
		RequestID: msg.RequestID,
		Payload: map[string]interface{}{
			"status": "success",
		},
		Timestamp: time.Now().UnixMilli(),
	}
	h.sendMessage(conn, ack)

	logger.Info("已读回执事件已发布",
		logger.StringField("reader_id", conn.UserID),
		logger.StringField("chat_id", readPayload.ChatID),
		logger.StringField("message_id", readPayload.MessageID))
}

// sendMessage 向连接发送消息
func (h *Handler) sendMessage(conn *Connection, msg OutgoingMessage) {
	data, err := json.Marshal(msg)
	if err != nil {
		logger.Error("序列化响应消息失败", logger.ErrorField(err))
		return
	}

	select {
	case conn.Send <- data:
	default:
		logger.Warn("发送通道已满，丢弃消息", logger.StringField("session_id", conn.SessionID))
	}
}

// sendError 向连接发送错误消息
func (h *Handler) sendError(conn *Connection, requestID string, code int32, message string) {
	response := OutgoingMessage{
		Type:      MessageTypeError,
		RequestID: requestID,
		Payload: ErrorPayload{
			Code:    code,
			Message: message,
		},
		Timestamp: time.Now().UnixMilli(),
	}

	h.sendMessage(conn, response)
}

// cleanup 清理连接
func (h *Handler) cleanup(conn *Connection) {
	conn.mu.Lock()
	if conn.IsClosed {
		conn.mu.Unlock()
		return
	}
	conn.IsClosed = true
	close(conn.Send) // 在锁定状态下就关闭 channel
	conn.mu.Unlock()

	if conn.SessionID != "" {
		h.manager.RemoveConnection(conn.SessionID)
		h.unified.Unregister(conn.SessionID) // 从 unified 管理器中也注销
	}

	if conn.UserID != "" && h.eventBus != nil {
		presenceEvent := &types.UserPresenceEvent{
			UserID:    conn.UserID,
			DeviceID:  conn.DeviceID,
			Online:    false,
			Timestamp: time.Now(),
		}
		if err := h.eventBus.PublishPresenceEvent(h.ctx, presenceEvent); err != nil {
			logger.Warn("发布用户离线事件失败", logger.ErrorField(err))
		}
	}

	conn.Conn.Close()

	logger.Info("WebSocket 已断开",
		logger.StringField("user_id", conn.UserID),
		logger.StringField("session_id", conn.SessionID))
}

// Close 关闭 Handler 并释放资源
func (h *Handler) Close() error {
	h.cancel()
	if h.eventBus != nil {
		_ = h.eventBus.Close()
	}
	return nil
}
