package websocket

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"Logos/internal/service/messaging/types"
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
	jwtManager *jwt.JWTManager
	eventBus   *types.EventBus
	ctx        context.Context
	cancel     context.CancelFunc
}

// NewHandler 创建新的 WebSocket 处理器（简化，无额外依赖）
func NewHandler() *Handler {
	ctx, cancel := context.WithCancel(context.Background())
	h := &Handler{
		manager:    NewConnectionManager(),
		jwtManager: jwt.NewJWTManager(),
		eventBus:   types.GetEventBus(),
		ctx:        ctx,
		cancel:     cancel,
	}
	h.startConsumers()
	return h
}

// startConsumers 启动 Kafka 消费者
func (h *Handler) startConsumers() {
	go func() {
		chatHandler := func(msg *mq.Message) error {
			return h.handleChatEvent(msg)
		}
		if err := h.eventBus.SubscribeChatEvents(h.ctx, chatHandler); err != nil {
			logger.Error("订阅Chat事件失败", logger.ErrorField(err))
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
	logger.Info("收到聊天事件", logger.StringField("topic", msg.Topic))

	// 先尝试解析为 TypingEvent
	if typingEvent, err := types.TypingEventFromJSON(msg.Value); err == nil && typingEvent.UserID != "" {
		// 只有当有 RecipientIDs 时才处理（这是 Chat Service 重新发布的完整事件）
		if len(typingEvent.RecipientIDs) > 0 {
			return h.handleTypingEvent(typingEvent)
		}
		logger.Debug("输入状态事件没有 RecipientIDs，跳过（等待 Chat Service 处理后重新发布）",
			logger.StringField("chat_id", typingEvent.ChatID),
			logger.StringField("user_id", typingEvent.UserID))
		return nil
	}

	// 然后尝试解析为 MessageReadEvent
	if readEvent, err := types.MessageReadEventFromJSON(msg.Value); err == nil && readEvent.ReaderID != "" {
		// 只有当有 RecipientIDs 时才处理
		if len(readEvent.RecipientIDs) > 0 {
			return h.handleMessageReadEvent(readEvent)
		}
		logger.Debug("已读回执事件没有 RecipientIDs，跳过",
			logger.StringField("chat_id", readEvent.ChatID),
			logger.StringField("reader_id", readEvent.ReaderID))
		return nil
	}

	// 否则解析为 MessageEvent
	event, err := types.MessageEventFromJSON(msg.Value)
	if err != nil {
		logger.Error("解析消息事件失败", logger.ErrorField(err))
		return err
	}

	// 如果是 MessageEvent 且有 RecipientIDs，则作为聊天消息转发
	if len(event.RecipientIDs) > 0 {
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

		h.broadcastToRelevantUsers(event, data)
	}

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

	// 优先使用 RecipientIDs 进行精确转发
	if len(event.RecipientIDs) > 0 {
		logger.Debug("使用 RecipientIDs 进行精确转发",
			logger.IntField("count", len(event.RecipientIDs)))
		for _, uid := range event.RecipientIDs {
			h.manager.SendMessageToUser(uid, data)
		}
	} else {
		// 回退逻辑：根据会话类型确定转发目标
		if strings.HasPrefix(event.ChatID, "private_") {
			// 单聊：解析出对方用户 ID 并精确转发
			h.forwardReadReceiptForPrivateChat(event, data)
		} else {
			// 群聊/广播/其他：使用广播给除阅读者外的所有人
			logger.Debug("群聊/其他类型会话，使用广播转发已读回执",
				logger.StringField("chat_id", event.ChatID))
			h.manager.BroadcastMessageExcept(data, event.ReaderID)
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

	// 优先使用 RecipientIDs 进行精确转发
	if len(event.RecipientIDs) > 0 {
		logger.Debug("使用 RecipientIDs 进行精确转发",
			logger.IntField("count", len(event.RecipientIDs)))
		for _, uid := range event.RecipientIDs {
			h.manager.SendMessageToUser(uid, data)
		}
	} else {
		// 回退逻辑
		if strings.HasPrefix(event.ChatID, "private_") {
			// 单聊：解析出对方
			h.forwardTypingForPrivateChat(event, data)
		} else {
			// 群聊/其他：广播给除输入者外的所有人
			logger.Debug("群聊/其他类型会话，使用广播转发输入状态",
				logger.StringField("chat_id", event.ChatID))
			h.manager.BroadcastMessageExcept(data, event.UserID)
		}
	}

	logger.Info("输入状态已转发",
		logger.StringField("user_id", event.UserID),
		logger.StringField("chat_id", event.ChatID))

	return nil
}

// forwardTypingForPrivateChat 处理单聊输入状态的精确转发
func (h *Handler) forwardTypingForPrivateChat(event *types.TypingEvent, data []byte) {
	// 单聊 ChatID 格式: private_{userID1}_{userID2}
	parts := strings.Split(event.ChatID, "_")
	if len(parts) != 3 {
		logger.Warn("单聊 ChatID 格式错误", logger.StringField("chat_id", event.ChatID))
		h.manager.BroadcastMessageExcept(data, event.UserID)
		return
	}

	user1 := parts[1]
	user2 := parts[2]

	var otherUser string
	if event.UserID == user1 {
		otherUser = user2
	} else if event.UserID == user2 {
		otherUser = user1
	} else {
		logger.Warn("输入用户不在单聊会话中",
			logger.StringField("user_id", event.UserID),
			logger.StringField("chat_id", event.ChatID))
		return
	}

	h.manager.SendMessageToUser(otherUser, data)
	logger.Debug("输入状态已精确转发给单聊对方",
		logger.StringField("user_id", event.UserID),
		logger.StringField("recipient_id", otherUser),
		logger.StringField("chat_id", event.ChatID))
}

// forwardReadReceiptForPrivateChat 处理单聊已读回执的精确转发
func (h *Handler) forwardReadReceiptForPrivateChat(event *types.MessageReadEvent, data []byte) {
	// 单聊 ChatID 格式: private_{userID1}_{userID2}
	parts := strings.Split(event.ChatID, "_")
	if len(parts) != 3 {
		logger.Warn("单聊 ChatID 格式错误", logger.StringField("chat_id", event.ChatID))
		// 回退到广播
		h.manager.BroadcastMessageExcept(data, event.ReaderID)
		return
	}

	user1 := parts[1]
	user2 := parts[2]

	// 确定对方是谁
	var otherUser string
	if event.ReaderID == user1 {
		otherUser = user2
	} else if event.ReaderID == user2 {
		otherUser = user1
	} else {
		logger.Warn("阅读者不在单聊会话中",
			logger.StringField("reader_id", event.ReaderID),
			logger.StringField("chat_id", event.ChatID))
		return
	}

	// 精确转发给对方
	h.manager.SendMessageToUser(otherUser, data)
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
		h.manager.BroadcastMessage(data)
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
		h.manager.SendMessageToUser(event.UserID, data)
	}
	return nil
}

// broadcastToRelevantUsers 根据事件中的接收者列表转发消息
func (h *Handler) broadcastToRelevantUsers(event *types.MessageEvent, data []byte) {
	// 如果事件中包含接收者列表，直接使用
	if len(event.RecipientIDs) > 0 {
		for _, userID := range event.RecipientIDs {
			h.manager.SendMessageToUser(userID, data)
		}
		logger.Debug("message broadcast completed",
			logger.StringField("chat_id", event.ChatID),
			logger.IntField("recipient_count", len(event.RecipientIDs)))
		return
	}

	// 降级处理：广播给除发送者外的所有在线用户
	logger.Warn("no recipient_ids in event, fallback to broadcast",
		logger.StringField("chat_id", event.ChatID))
	h.manager.BroadcastMessageExcept(data, event.SenderID)
}

// HandleWebSocket 处理 WebSocket 升级和连接
func (h *Handler) HandleWebSocket(c *gin.Context) {
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

	conn.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
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
	}
}

// writePump 向 WebSocket 连接写入消息
func (h *Handler) writePump(conn *Connection, wg *sync.WaitGroup) {
	defer func() {
		wg.Done()
		h.cleanup(conn)
	}()

	ticker := time.NewTicker(30 * time.Second)
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
		[]string{},
		[]string{},
	)

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
	conn.mu.Unlock()

	if conn.SessionID != "" {
		h.manager.RemoveConnection(conn.SessionID)
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

// GetManager 获取连接管理器
func (h *Handler) GetManager() *ConnectionManager {
	return h.manager
}
