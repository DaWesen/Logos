package websocket

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"

	"Logos/config"
	"Logos/internal/service/messaging/types"
	"Logos/internal/service/platform/gateway"
	"Logos/internal/service/platform/gateway/downlink"
	"Logos/pkg/auth"
	"Logos/pkg/cache"
	"Logos/pkg/client"
	"Logos/pkg/jwt"
	"Logos/pkg/logger"
	"Logos/pkg/mq"
	"Logos/pkg/obs"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     checkOrigin,
}

// wsAuthSubprotocol 前端通过子协议把令牌传给网关：`new WebSocket(url, ["access_token", token])`。
// 相比 `?token=`，令牌不会出现在 URL，因而不会进 access log、浏览器历史或 Referer。
const wsAuthSubprotocol = "access_token"

// wsTokenFromSubprotocol 从 Sec-WebSocket-Protocol 头里取 token。
// 头形如 `access_token, <jwt>`，按逗号切分后取协议名后面那个元素。
func wsTokenFromSubprotocol(r *http.Request) string {
	parts := strings.Split(r.Header.Get("Sec-WebSocket-Protocol"), ",")
	for i, p := range parts {
		if strings.TrimSpace(p) == wsAuthSubprotocol && i+1 < len(parts) {
			return strings.TrimSpace(parts[i+1])
		}
	}
	return ""
}

// checkOrigin 校验 WebSocket 请求来源
// 未配置白名单（LOGOS_WS_ALLOWED_ORIGINS 为空）时放行，兼容本地开发；
// 配置后仅允许白名单内的 Origin，防止跨站 WebSocket 劫持。
func checkOrigin(r *http.Request) bool {
	allowed := config.GetConfig().GetGatewayAllowedOrigins()
	if len(allowed) == 0 {
		return true
	}

	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	for _, a := range allowed {
		if a == "*" || strings.EqualFold(a, origin) {
			return true
		}
	}

	logger.Warn("WebSocket Origin 被拒绝", logger.StringField("origin", origin))
	return false
}

// Handler 处理 WebSocket 连接（更简化，专注于连接管理和转发）
type Handler struct {
	manager    *ConnectionManager
	unified    *gateway.UnifiedConnectionManager
	jwtManager *jwt.JWTManager
	revoker    *auth.Revoker
	eventBus   *types.EventBus
	presence   *gateway.PresenceTracker
	enricher   *gateway.SenderEnricher
	ctx        context.Context
	cancel     context.CancelFunc
}

func NewHandler() *Handler {
	ctx, cancel := context.WithCancel(context.Background())
	redisCache := cache.NewRedisCache()
	eventBus := types.GetEventBus()
	h := &Handler{
		manager:    NewConnectionManager(),
		unified:    gateway.GetUnifiedConnectionManager(),
		jwtManager: jwt.NewJWTManager(),
		revoker:    auth.NewRevoker(redisCache),
		eventBus:   eventBus,
		presence:   gateway.NewPresenceTracker(gateway.NewRedisPresenceStore(redisCache), eventBus),
		ctx:        ctx,
		cancel:     cancel,
	}
	h.startConsumers()
	return h
}

func (h *Handler) SetUserClient(uc *client.UserClient) {
	if uc == nil {
		h.enricher = nil
		return
	}
	h.enricher = gateway.NewSenderEnricher(uc)
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

	case types.EventTypeMessageDelete:
		deleteEvent, err := types.MessageDeleteEventFromJSON(msg.Value)
		if err != nil {
			logger.Error("解析删除事件失败", logger.ErrorField(err))
			return err
		}
		return h.handleMessageDeleteEvent(deleteEvent)

	case types.EventTypeMessageEdit:
		editEvent, err := types.MessageEditEventFromJSON(msg.Value)
		if err != nil {
			logger.Error("解析编辑事件失败", logger.ErrorField(err))
			return err
		}
		return h.handleMessageEditEvent(editEvent)

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

		h.enricher.Enrich(h.ctx, event)

		if event.ChatType == types.ChatTypePrivate {
			logger.Debug("收到 outgoing 私聊消息",
				logger.StringField("msg_id", event.ID),
				logger.StringField("time", time.Now().Format("2006-01-02 15:04:05.000000")))
		}

		data, err := downlink.Message(event).Marshal()
		if err != nil {
			logger.Error("序列化事件失败", logger.ErrorField(err))
			return err
		}

		// 推送到前端 WebSocket
		if event.ChatType == types.ChatTypePrivate {
			logger.Debug("推送私聊消息到 WebSocket",
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

	data, err := downlink.Withdraw(event).Marshal()
	if err != nil {
		logger.Error("序列化撤回事件失败", logger.ErrorField(err))
		return err
	}

	if !downlink.DeliverToChat(h.unified, data, event.RecipientIDs, event.SenderID, event.ChatID, event.ChatType == types.ChatTypeBroadcast) {
		logger.Warn("撤回事件缺少可投递的对端，已丢弃",
			logger.StringField("chat_id", event.ChatID),
			logger.StringField("message_id", event.MessageID))
		return nil
	}

	logger.Info("撤回通知已转发",
		logger.StringField("message_id", event.MessageID),
		logger.IntField("recipient_count", len(event.RecipientIDs)))

	return nil
}

// handleMessageDeleteEvent 处理消息删除事件。
// 删除是「用户私有状态」，只推给删除者本人的全部在线设备；缺失 RecipientIDs 时按 OwnerID 兜底，
// 绝不回退成会话内广播（否则会向其他成员泄露「谁删了哪条消息」）。
func (h *Handler) handleMessageDeleteEvent(event *types.MessageDeleteEvent) error {
	recipients := event.RecipientIDs
	if len(recipients) == 0 && event.OwnerID != "" {
		recipients = []string{event.OwnerID}
	}
	if len(recipients) == 0 {
		logger.Warn("删除事件缺少接收者，跳过",
			logger.StringField("chat_id", event.ChatID),
			logger.StringField("message_id", event.MessageID))
		return nil
	}

	data, err := downlink.Delete(event).Marshal()
	if err != nil {
		logger.Error("序列化删除事件失败", logger.ErrorField(err))
		return err
	}

	if !downlink.DeliverToUsers(h.unified, recipients, data) {
		logger.Warn("删除事件未投递", logger.StringField("message_id", event.MessageID))
		return nil
	}

	logger.Info("删除通知已转发（仅本人多端）",
		logger.StringField("message_id", event.MessageID),
		logger.StringField("owner_id", event.OwnerID),
		logger.IntField("recipient_count", len(recipients)))
	return nil
}

// handleMessageEditEvent 把编辑后的内容转发给会话内所有成员（含发送者自己的其他在线设备）。
// 接收端按 version 幂等应用，乱序/重复投递不会把新内容改回旧内容。
// 缺少 RecipientIDs 时直接跳过而不是广播：广播会把私聊内容推给无关用户。
func (h *Handler) handleMessageEditEvent(event *types.MessageEditEvent) error {
	if len(event.RecipientIDs) == 0 {
		logger.Warn("编辑事件缺少接收者，跳过",
			logger.StringField("chat_id", event.ChatID),
			logger.StringField("message_id", event.MessageID))
		return nil
	}

	data, err := downlink.Edit(event).Marshal()
	if err != nil {
		logger.Error("序列化编辑事件失败", logger.ErrorField(err))
		return err
	}

	if !downlink.DeliverToUsers(h.unified, event.RecipientIDs, data) {
		logger.Warn("编辑事件未投递", logger.StringField("message_id", event.MessageID))
		return nil
	}

	logger.Info("编辑通知已转发",
		logger.StringField("message_id", event.MessageID),
		logger.IntField("version", int(event.Version)),
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

	data, err := downlink.ReadReceipt(event).Marshal()
	if err != nil {
		logger.Error("序列化已读事件失败", logger.ErrorField(err))
		return err
	}

	if !downlink.DeliverToChat(h.unified, data, event.RecipientIDs, event.ReaderID, event.ChatID, event.ChatType == types.ChatTypeBroadcast) {
		logger.Warn("已读回执缺少可投递的对端，已丢弃",
			logger.StringField("chat_id", event.ChatID),
			logger.StringField("reader_id", event.ReaderID))
		return nil
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

	data, err := downlink.Typing(event).Marshal()
	if err != nil {
		logger.Error("序列化输入状态事件失败", logger.ErrorField(err))
		return err
	}

	// TypingEvent 不带 chat_type：仅靠会话 ID 识别广播会话，群聊缺名单一律丢弃。
	if !downlink.DeliverToChat(h.unified, data, event.RecipientIDs, event.UserID, event.ChatID, false) {
		logger.Warn("输入状态缺少可投递的对端，已丢弃",
			logger.StringField("chat_id", event.ChatID),
			logger.StringField("user_id", event.UserID))
		return nil
	}

	logger.Info("输入状态已转发",
		logger.StringField("user_id", event.UserID),
		logger.StringField("chat_id", event.ChatID))

	return nil
}

// handleIMEvent 处理IM事件
func (h *Handler) handleIMEvent(msg *mq.Message) error {
	logger.Info("收到IM事件", logger.StringField("topic", msg.Topic))
	event, err := types.UserPresenceEventFromJSON(msg.Value)
	if err != nil {
		logger.Error("解析用户在线事件失败", logger.ErrorField(err))
		return err
	}

	data, err := downlink.Presence(event).Marshal()
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

	data, err := downlink.New(downlink.TypeMessage, event).Marshal()
	if err != nil {
		logger.Error("序列化事件失败", logger.ErrorField(err))
		return err
	}

	if event.UserID != "" {
		h.unified.SendToUser(event.UserID, data)
	}
	return nil
}

// broadcastToRelevantUsers 根据事件中的接收者列表转发消息；私聊兜底只发给会话双方，绝不广播。
func (h *Handler) broadcastToRelevantUsers(event *types.MessageEvent, data []byte) {
	// 消息与会话事件（已读/输入状态/撤回）的差别：私聊兜底时要连同发送者本人一起投递，
	// 否则发送者的其他在线设备收不到自己刚发出的消息。
	if !downlink.DeliverMessage(h.unified, data, event.RecipientIDs, event.SenderID, event.ChatID, event.ChatType == types.ChatTypeBroadcast) {
		logger.Warn("聊天消息缺少可投递的对端，已丢弃",
			logger.StringField("chat_id", event.ChatID),
			logger.StringField("msg_id", event.ID))
		return
	}
	logger.Debug("message broadcast completed",
		logger.StringField("chat_id", event.ChatID),
		logger.IntField("recipient_count", len(event.RecipientIDs)))
}

// HandleWebSocket 处理 WebSocket 升级和连接
func (h *Handler) HandleWebSocket(c *gin.Context) {
	// 首选子协议传令牌；仍保留 URL query 兜底以兼容旧客户端，但会记警告，后续版本删除。
	token := wsTokenFromSubprotocol(c.Request)
	authSource := "subprotocol"
	if token == "" {
		token = c.Query("token")
		authSource = "query"
	}

	// 仅在客户端请求了该子协议时才回写，否则浏览器会因"服务端选了未请求的协议"而断开。
	u := upgrader
	if strings.Contains(c.Request.Header.Get("Sec-WebSocket-Protocol"), wsAuthSubprotocol) {
		u.Subprotocols = []string{wsAuthSubprotocol}
	}
	conn, err := u.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		logger.Error("WebSocket 升级失败", logger.ErrorField(err))
		return
	}

	wsConn := &Connection{
		Conn:      conn,
		Send:      make(chan []byte, 256),
		IsClosed:  false,
		done:      make(chan struct{}),
		createdAt: time.Now(),
		limiter:   gateway.NewInboundTokenBucket(),
	}
	// 所有认证方式（子协议 / connect 消息）都必须先受单帧大小上限保护，
	// 否则未认证连接也能用一个超大帧把内存打满。
	applyInboundFrameLimit(conn)

	if authSource == "query" && token != "" {
		logger.Warn("WebSocket 通过 URL query 传递令牌（已弃用，请改用 Sec-WebSocket-Protocol）")
	}

	if token != "" {
		claims, err := h.jwtManager.ParseToken(token)
		if err != nil {
			obs.RecordGatewayAuthFailure(c.Request.Context(), "websocket", string(gateway.AuthFailureInvalid))
			logger.Warn("WebSocket token 认证失败", logger.ErrorField(err))
			h.sendError(wsConn, "", gateway.CodeUnauthorized, "令牌无效")
			wsConn.Conn.WriteMessage(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "authentication failed"))
			wsConn.Conn.Close()
			return
		}
		// 已登出的令牌不允许建立长连接（否则 HTTP 侧撤销对 WS 不生效）
		if h.revoker.IsRevoked(c.Request.Context(), claims) {
			obs.RecordGatewayAuthFailure(c.Request.Context(), "websocket", string(gateway.AuthFailureRevoked))
			logger.Warn("WebSocket token 已撤销", logger.StringField("user_id", claims.UserID))
			h.sendError(wsConn, "", gateway.CodeUnauthorized, "令牌已失效")
			wsConn.Conn.WriteMessage(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "authentication failed"))
			wsConn.Conn.Close()
			return
		}

		sessionID := uuid.New().String()
		wsConn.UserID = claims.UserID
		wsConn.SessionID = sessionID
		// 保存声明供定期复核使用（撤销状态与过期时间都在 claims 上）。
		wsConn.claims = claims
		obs.RecordGatewayConnection(h.ctx, "websocket", "open")
		h.evictConnection(h.manager.AddConnection(sessionID, wsConn))
		h.unified.Register(sessionID, claims.UserID, "web", "websocket", func(data []byte) {
			pushToConn(wsConn, data)
		})

		// 只有该用户全网首个会话才发 online：多设备/多节点登录不会重复广播
		h.presence.Online(h.ctx, claims.UserID, "web", sessionID)

		response := OutgoingMessage{
			Type: MessageTypeConnect,
			Payload: ConnectResponsePayload{
				SessionID: sessionID,
			},
			Timestamp: time.Now().UnixMilli(),
		}
		h.sendMessage(wsConn, response)

		logger.Info("WebSocket 已认证连接",
			logger.StringField("user_id", wsConn.UserID),
			logger.StringField("session_id", sessionID),
			logger.StringField("auth_source", authSource))
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
		conn.Conn.SetReadDeadline(time.Now().Add(gateway.WSAuthTimeout))
	} else {
		conn.Conn.SetReadDeadline(time.Now().Add(gateway.WSReadTimeout))
	}
	conn.Conn.SetPongHandler(func(string) error {
		conn.Conn.SetReadDeadline(time.Now().Add(gateway.WSReadTimeout))
		// pong 说明连接仍存活：续期在线会话，避免存活连接被当作过期会话剔除。
		// 续期走本地节流，避免每个 pong 都打两次 Redis。
		h.touchPresence(conn)
		return nil
	})

	for {
		_, msg, err := conn.Conn.ReadMessage()
		if err != nil {
			// 超限错误单独告警：它是协议违规/攻击信号，不应淹没在普通断连日志里。
			if errors.Is(err, websocket.ErrReadLimit) {
				logger.Warn("WebSocket 上行帧超过大小限制，断开连接",
					logger.StringField("user_id", conn.UserID),
					logger.StringField("session_id", conn.SessionID),
					logger.IntField("limit_bytes", inboundFrameLimit))
			} else if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				logger.Error("WebSocket 读取错误", logger.ErrorField(err))
			}
			break
		}

		// 限流发生在解析之前：恶意客户端的高频帧不应消耗 json.Unmarshal 与
		// Kafka 生产链路的资源，被拒时直接回 429 并跳过本次读取的续期。
		if !conn.limiter.Allow() {
			gateway.NoteInboundRateLimit("websocket", conn.SessionID, conn.UserID)
			h.sendError(conn, "", gateway.CodeTooManyRequests, "上行消息过于频繁，请稍后重试")
			continue
		}

		h.handleMessage(conn, msg)

		if conn.UserID != "" {
			conn.Conn.SetReadDeadline(time.Now().Add(gateway.WSReadTimeout))
		}
	}
}

// writePump 向 WebSocket 连接写入消息
func (h *Handler) writePump(conn *Connection, wg *sync.WaitGroup) {
	defer func() {
		wg.Done()
		h.cleanup(conn)
	}()

	// ping 周期带 ±10% 抖动：网关重启后所有连接会在同一秒建立，
	// 固定周期会让它们永远保持同一相位，形成周期性的全员 ping 尖峰。
	pingInterval := gateway.JitterDuration(gateway.WSPingInterval, gateway.PingJitterFraction, rand.Float64())
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-conn.done:
			// 连接已关闭：尽力发送一次关闭帧，保持正常的关闭握手。
			// 停机路径可能已经写过 CloseGoingAway，这里重复写只会得到一次错误，忽略即可。
			conn.writeMu.Lock()
			_ = conn.Conn.SetWriteDeadline(time.Now().Add(time.Second))
			_ = conn.Conn.WriteMessage(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			conn.writeMu.Unlock()
			return
		case msg := <-conn.Send:
			// 每次写前重置截止时间：否则一次卡死的写会永久挂住 writePump，
			// 连接既不会关闭也不会再消费 Send，缓冲很快被填满并拖垮扇出。
			conn.writeMu.Lock()
			_ = conn.Conn.SetWriteDeadline(time.Now().Add(gateway.WSWriteTimeout))
			w, err := conn.Conn.NextWriter(websocket.TextMessage)
			if err != nil {
				conn.writeMu.Unlock()
				return
			}
			_, _ = w.Write(msg)
			err = w.Close()
			conn.writeMu.Unlock()
			if err != nil {
				return
			}

		case <-ticker.C:
			// 心跳是唯一「稳定周期性」的时点：借它做令牌复核，不必额外起一个
			// 每连接独立的 timer。复核周期（5 分钟）远大于 ping 周期（60s），
			// 因此绝大多数 tick 只做一次时间比较。
			if !h.recheckAuth(conn) {
				return
			}
			conn.writeMu.Lock()
			err := conn.Conn.SetWriteDeadline(time.Now().Add(gateway.WSWriteTimeout))
			if err == nil {
				err = conn.Conn.WriteMessage(websocket.PingMessage, nil)
			}
			conn.writeMu.Unlock()
			if err != nil {
				return
			}
		}
	}
}

// errorFrame 把错误响应序列化成一帧下行数据（复核失败时直接写 socket，不走 Send 队列，
// 因为此时连接马上就要关闭，排队发送存在被 done 分支抢先而丢失的可能）。
func errorFrame(requestID string, code int32, message string) []byte {
	data, err := json.Marshal(OutgoingMessage{
		Type:      MessageTypeError,
		RequestID: requestID,
		Payload:   ErrorPayload{Code: code, Message: message},
		Timestamp: time.Now().UnixMilli(),
	})
	if err != nil {
		return nil
	}
	return data
}

// recheckAuth 在心跳时点复核长连接持有的令牌是否仍然有效。
//
// 返回 false 表示连接已被判定失效：调用方应立即退出 writePump（defer 里会走
// cleanup 释放会话、注销 unified、发 offline）。
//
// 复核失败必须主动断开而不是只记日志：HTTP 侧已经拒绝该令牌，长连接继续可用
// 等于给已登出/已改密的用户留了一条后门，且这条连接的每次上行都会写 Kafka。
func (h *Handler) recheckAuth(conn *Connection) bool {
	now := time.Now()
	if conn.UserID == "" {
		// 未认证连接由 WSAuthTimeout 负责回收，这里不参与复核。
		return true
	}
	if !gateway.AuthRecheckDue(conn.lastAuthCheck, now, gateway.AuthRecheckInterval) {
		return true
	}
	conn.lastAuthCheck = now

	state, checkErr := h.revoker.Check(h.ctx, conn.claims)
	obs.RecordGatewayRevocationCheck(h.ctx, "websocket", state.String())
	if checkErr != nil {
		// 撤销查询失败时按 fail-open 继续：一次 Redis 抖动把全部长连接踢下线，
		// 代价远大于「已撤销令牌多存活一个复核周期」。unknown 指标用于告警。
		logger.Warn("WebSocket 令牌复核查询失败，按 fail-open 放行",
			logger.StringField("user_id", conn.UserID),
			logger.ErrorField(checkErr))
	}
	reason := gateway.ClassifyAuthFailure(conn.claims, state == auth.RevocationRevoked, now)
	if reason == gateway.AuthFailureNone {
		return true
	}

	obs.RecordGatewayAuthFailure(h.ctx, "websocket", string(reason))
	logger.Warn("WebSocket 长连接令牌复核失败，主动断开",
		logger.StringField("user_id", conn.UserID),
		logger.StringField("session_id", conn.SessionID),
		logger.StringField("reason", string(reason)))

	conn.setCloseEvent("auth")

	if conn.Conn != nil {
		conn.writeMu.Lock()
		_ = conn.Conn.SetWriteDeadline(time.Now().Add(time.Second))
		if frame := errorFrame("", gateway.CodeUnauthorized, "令牌已失效，请重新登录"); frame != nil {
			_ = conn.Conn.WriteMessage(websocket.TextMessage, frame)
		}
		_ = conn.Conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.ClosePolicyViolation, string(reason)))
		conn.writeMu.Unlock()
	}

	return false
}

// shutdownWriteTimeout 停机时为关闭帧预留的写超时。
// 停机路径不能等完整的 WSWriteTimeout（10s）：一条卡死连接会把整个进程的
// 优雅停机窗口（通常 5s）吃光，反而让其余连接被强杀。
const shutdownWriteTimeout = time.Second

// shutdownPollInterval 排空轮询间隔：用来观察 manager 是否已清空。
const shutdownPollInterval = 20 * time.Millisecond

// Shutdown 优雅停机：给每条在线连接发送 CloseGoingAway 关闭帧并注销会话，
// 在 ctx 内等待连接全部退出。返回等待结束时仍未退出的连接数（0 表示全部排空）。
//
// 为什么不能直接 Close 监听器就退出：SIGTERM 后进程若立刻消失，客户端看到的是
// 「连接被重置」，重连往往走指数退避（可能几分钟）；发送 1001 GoingAway 则明确
// 告诉客户端「服务端主动下线」，标准客户端会立刻发起重连，滚动发布期间用户无感。
func (h *Handler) Shutdown(ctx context.Context) int {
	conns := h.manager.AllConnections()
	for _, conn := range conns {
		h.closeGoingAway(conn)
	}

	for {
		remaining := h.manager.Len()
		if remaining == 0 {
			return 0
		}
		select {
		case <-ctx.Done():
			logger.Warn("WebSocket 停机排空超时，仍有连接未退出",
				logger.IntField("remaining", remaining))
			return remaining
		case <-time.After(shutdownPollInterval):
		}
	}
}

// closeGoingAway 给单条连接发送「服务端下线」关闭帧并走正常清理流程。
func (h *Handler) closeGoingAway(conn *Connection) {
	conn.mu.Lock()
	if conn.IsClosed {
		conn.mu.Unlock()
		return
	}
	conn.mu.Unlock()

	// 先标记来源再写帧：cleanup 读取 closeEvent 时不会与写帧竞争。
	conn.setCloseEvent("shutdown")

	conn.writeMu.Lock()
	if conn.Conn != nil {
		_ = conn.Conn.SetWriteDeadline(time.Now().Add(shutdownWriteTimeout))
		_ = conn.Conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseGoingAway, "gateway shutting down"))
	}
	conn.writeMu.Unlock()

	h.cleanup(conn)
}

// handleMessage 处理接收的 WebSocket 消息
func (h *Handler) handleMessage(conn *Connection, rawMsg []byte) {
	var msg IncomingMessage
	if err := json.Unmarshal(rawMsg, &msg); err != nil {
		logger.Warn("解析 WebSocket 消息失败", logger.ErrorField(err))
		h.sendError(conn, msg.RequestID, gateway.CodeBadRequest, "消息格式无效")
		return
	}

	if conn.UserID == "" && msg.Type != MessageTypeConnect {
		h.sendError(conn, msg.RequestID, gateway.CodeUnauthorized, "请先认证")
		return
	}

	// request_id 是所有上行帧的公共字段（用于 ACK 关联与幂等落库），
	// 统一在分发前校验，避免每个 handler 各写一份、新增类型时漏掉。
	if v := gateway.ValidateRequestID(msg.RequestID); v != nil {
		h.sendError(conn, msg.RequestID, gateway.CodeBadRequest, v.Error())
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
		h.sendError(conn, msg.RequestID, gateway.CodeBadRequest, "未知消息类型")
	}
}

// handleConnect 处理连接初始化
func (h *Handler) handleConnect(conn *Connection, msg *IncomingMessage) {
	if conn.UserID != "" {
		h.sendError(conn, msg.RequestID, gateway.CodeBadRequest, "已经认证")
		return
	}

	payloadBytes, err := json.Marshal(msg.Payload)
	if err != nil {
		h.sendError(conn, msg.RequestID, gateway.CodeBadRequest, "载荷无效")
		return
	}

	var payload ConnectPayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		h.sendError(conn, msg.RequestID, gateway.CodeBadRequest, "载荷格式无效")
		return
	}

	if payload.Token == "" {
		obs.RecordGatewayAuthFailure(h.ctx, "websocket", string(gateway.AuthFailureInvalid))
		h.sendError(conn, msg.RequestID, gateway.CodeUnauthorized, "缺少令牌")
		conn.Conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "authentication required"))
		conn.Conn.Close()
		return
	}

	claims, err := h.jwtManager.ParseToken(payload.Token)
	if err != nil {
		obs.RecordGatewayAuthFailure(h.ctx, "websocket", string(gateway.AuthFailureInvalid))
		h.sendError(conn, msg.RequestID, gateway.CodeUnauthorized, "令牌无效")
		conn.Conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "authentication failed"))
		conn.Conn.Close()
		return
	}
	state, checkErr := h.revoker.Check(h.ctx, claims)
	obs.RecordGatewayRevocationCheck(h.ctx, "websocket", state.String())
	if checkErr != nil {
		logger.Warn("WebSocket 建连时撤销查询失败，按 fail-open 放行",
			logger.StringField("user_id", claims.UserID),
			logger.ErrorField(checkErr))
	}
	if state == auth.RevocationRevoked {
		obs.RecordGatewayAuthFailure(h.ctx, "websocket", string(gateway.AuthFailureRevoked))
		h.sendError(conn, msg.RequestID, gateway.CodeUnauthorized, "令牌已失效")
		conn.Conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "authentication failed"))
		conn.Conn.Close()
		return
	}

	sessionID := uuid.New().String()
	conn.UserID = claims.UserID
	conn.DeviceID = payload.DeviceID
	conn.SessionID = sessionID
	// 保存声明供定期复核使用。
	conn.claims = claims

	obs.RecordGatewayConnection(h.ctx, "websocket", "open")
	h.evictConnection(h.manager.AddConnection(sessionID, conn))
	h.unified.Register(sessionID, claims.UserID, payload.DeviceID, "websocket", func(data []byte) {
		pushToConn(conn, data)
	})

	// 只有该用户全网首个会话才发 online：多设备/多节点登录不会重复广播
	h.presence.Online(h.ctx, conn.UserID, conn.DeviceID, sessionID)

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
	// 客户端心跳同样是在线会话的续期信号（同样走节流）
	h.touchPresence(conn)

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
		h.sendError(conn, msg.RequestID, gateway.CodeUnauthorized, "未连接")
		return
	}

	// 解析 payload
	payloadBytes, err := json.Marshal(msg.Payload)
	if err != nil {
		h.sendError(conn, msg.RequestID, gateway.CodeBadRequest, "载荷无效")
		return
	}

	var messagePayload MessagePayload
	if err := json.Unmarshal(payloadBytes, &messagePayload); err != nil {
		h.sendError(conn, msg.RequestID, gateway.CodeBadRequest, "载荷格式无效")
		return
	}

	// 上行公共校验：与 TCP 共用同一套规则，避免两条协议行为漂移。
	if v := gateway.ValidateChatID(messagePayload.ChatID); v != nil {
		h.sendError(conn, msg.RequestID, gateway.CodeBadRequest, v.Error())
		return
	}
	if v := gateway.ValidateContent(messagePayload.Content); v != nil {
		h.sendError(conn, msg.RequestID, gateway.CodeBadRequest, v.Error())
		return
	}
	if v := gateway.ValidateMentionIDs(messagePayload.MentionUserIDs); v != nil {
		h.sendError(conn, msg.RequestID, gateway.CodeBadRequest, v.Error())
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

	// 生成消息ID：携带客户端幂等键时派生确定性 ID，
	// 同一 (sender, request_id) 无论重发多少次都映射到同一个消息 ID，
	// 从而复用落库主键幂等与客户端按 ID 去重，避免重复消息。
	messageID := uuid.New().String()
	if msg.RequestID != "" {
		messageID = gateway.DeriveMessageID(conn.UserID, msg.RequestID)
	}

	// 前端发送消息链路开始（仅记录长度，不落消息明文）
	if messagePayload.ChatType == 1 {
		logger.Debug("收到私聊消息",
			logger.StringField("msg_id", messageID),
			logger.StringField("sender_id", conn.UserID),
			logger.StringField("chat_id", messagePayload.ChatID),
			logger.IntField("content_len", len(messagePayload.Content)),
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
	// 透传幂等键：Chat 服务据此按 (sender_id, request_id) 幂等落库，
	// 与 REST 入口共用同一张幂等表，保证跨链路重发不产生重复消息。
	event.RequestID = msg.RequestID

	// 发布到 Kafka
	if messagePayload.ChatType == 1 {
		logger.Debug("发布私聊消息到 Kafka",
			logger.StringField("msg_id", messageID),
			logger.StringField("time", time.Now().Format("2006-01-02 15:04:05.000000")))
	}

	// 发布到事件总线
	if err := h.eventBus.PublishMessageEvent(h.ctx, event); err != nil {
		logger.Error("发布消息事件失败", logger.ErrorField(err))
		h.sendError(conn, msg.RequestID, gateway.CodeInternal, "发送消息失败")
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
		h.sendError(conn, msg.RequestID, gateway.CodeUnauthorized, "未连接")
		return
	}

	// 解析 payload
	payloadBytes, err := json.Marshal(msg.Payload)
	if err != nil {
		h.sendError(conn, msg.RequestID, gateway.CodeBadRequest, "载荷无效")
		return
	}

	var typingPayload TypingPayload
	if err := json.Unmarshal(payloadBytes, &typingPayload); err != nil {
		h.sendError(conn, msg.RequestID, gateway.CodeBadRequest, "载荷格式无效")
		return
	}

	if v := gateway.ValidateChatID(typingPayload.ChatID); v != nil {
		h.sendError(conn, msg.RequestID, gateway.CodeBadRequest, v.Error())
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
		h.sendError(conn, msg.RequestID, gateway.CodeInternal, "发送输入状态失败")
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
		h.sendError(conn, msg.RequestID, gateway.CodeUnauthorized, "未连接")
		return
	}

	// 解析 payload
	payloadBytes, err := json.Marshal(msg.Payload)
	if err != nil {
		h.sendError(conn, msg.RequestID, gateway.CodeBadRequest, "载荷无效")
		return
	}

	var readPayload ReadReceiptPayload
	if err := json.Unmarshal(payloadBytes, &readPayload); err != nil {
		h.sendError(conn, msg.RequestID, gateway.CodeBadRequest, "载荷格式无效")
		return
	}

	if v := gateway.ValidateChatID(readPayload.ChatID); v != nil {
		h.sendError(conn, msg.RequestID, gateway.CodeBadRequest, v.Error())
		return
	}
	if v := gateway.ValidateMessageIDs([]string{readPayload.MessageID}); v != nil {
		h.sendError(conn, msg.RequestID, gateway.CodeBadRequest, v.Error())
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
		h.sendError(conn, msg.RequestID, gateway.CodeInternal, "发送已读回执失败")
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

	// 控制帧与广播走同一套非阻塞投递策略：缓冲满时丢弃并计数，避免阻塞调用方。
	pushToConn(conn, data)
}

// sendError 向连接发送错误消息
func (h *Handler) sendError(conn *Connection, requestID string, code int32, message string) {
	response := OutgoingMessage{
		Type:      MessageTypeError,
		RequestID: requestID,
		Payload: ErrorPayload{
			Code:    code,
			Message: gateway.ErrorMessage(code, message),
		},
		Timestamp: time.Now().UnixMilli(),
	}

	h.sendMessage(conn, response)
}

// evictConnection 清理被「单用户连接数上限」淘汰的旧会话。
// 走与正常断连一致的 cleanup 流程：关闭底层连接、从 unified 注销、按 presence
// 语义发 offline（若该用户还有其他会话在线则不会误发）。
func (h *Handler) evictConnection(old *Connection) {
	if old == nil {
		return
	}
	obs.RecordGatewayConnection(h.ctx, "websocket", "evict")
	logger.Warn("WebSocket 单用户连接数超限，淘汰最旧会话",
		logger.StringField("user_id", old.UserID),
		logger.StringField("session_id", old.SessionID),
		logger.IntField("max_connections", maxConnectionsPerUser))
	old.setCloseEvent("evict")
	h.cleanup(old)
}

// touchPresence 按 PresenceTouchInterval 节流地续期在线会话。
//
// 不在每次 pong/心跳里直连 Redis：活跃客户端的心跳频率远高于「保证不被判过期」
// 所需的频率，逐次写会造成明显的读放大（set 之前还要 HGetAll 全量会话）。
// 节流后的最坏续期间隔仍远小于 PresenceStaleAfter（见不变式测试）。
func (h *Handler) touchPresence(conn *Connection) {
	now := time.Now()
	if !gateway.ShouldTouchPresence(conn.lastPresenceTouch, now, gateway.PresenceTouchInterval) {
		return
	}
	conn.lastPresenceTouch = now
	h.presence.Touch(h.ctx, conn.UserID, conn.SessionID)
}

// cleanup 清理连接
func (h *Handler) cleanup(conn *Connection) {
	conn.mu.Lock()
	if conn.IsClosed {
		conn.mu.Unlock()
		return
	}
	conn.IsClosed = true
	event := conn.closeEvent
	if event == "" {
		event = "close"
	}
	close(conn.done) // 关闭 done 而非 Send，使 writePump 退出且发送方永不 panic
	conn.mu.Unlock()

	// 只统计已认证会话的关闭：未认证连接不会出现在 manager 里，
	// 与 open 计数严格配对才能算出真实在线连接数。
	if conn.SessionID != "" {
		obs.RecordGatewayConnection(h.ctx, "websocket", event)
		h.manager.RemoveConnection(conn.SessionID)
		h.unified.Unregister(conn.SessionID) // 从 unified 管理器中也注销
	}

	// 只有该用户全网最后一个会话断开才发 offline：关掉手机端不会让桌面端变离线
	h.presence.Offline(h.ctx, conn.UserID, conn.DeviceID, conn.SessionID)

	// Conn 为空只出现在测试/降级构造路径（没有真实 socket），不能因此 panic。
	if conn.Conn != nil {
		_ = conn.Conn.Close()
	}

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
