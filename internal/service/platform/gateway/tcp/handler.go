package tcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"Logos/internal/service/messaging/types"
	"Logos/internal/service/platform/gateway"
	"Logos/internal/service/platform/gateway/downlink"
	"Logos/pkg/auth"
	"Logos/pkg/cache"
	"Logos/pkg/jwt"
	"Logos/pkg/logger"
	"Logos/pkg/obs"

	"github.com/google/uuid"
)

// MessageType 与 WebSocket 共用同一套下行类型。
type MessageType = downlink.Type

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

// IncomingMessage 上行帧。与 WS 一样用 data 作为载荷字段名，避免同一个协议两套字段。
type IncomingMessage struct {
	Type      MessageType     `json:"type"`
	RequestID string          `json:"request_id"`
	Payload   json.RawMessage `json:"data"`
}

// OutgoingMessage 是网关统一下行信封的别名。
type OutgoingMessage = downlink.Envelope

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
	// Typing 与 WS 一致：缺省 false 表示「停止输入」。
	Typing bool `json:"typing"`
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
	done      chan struct{} // 关闭信号；Send 永不关闭，避免并发发送到已关闭 channel 导致 panic
	// createdAt 连接建立时间：用户连接数超限时用它选最旧会话淘汰。
	createdAt time.Time
	// limiter 单连接上行令牌桶；nil 表示不限流。
	limiter *gateway.TokenBucket
	// claims 认证时解析出的令牌声明，供心跳时点定期复核撤销/过期状态。
	claims *jwt.Claims
	// lastAuthCheck 最近一次令牌复核时间；零值表示尚未复核。
	// 只在 readPump 心跳路径读写，与 writePump 无并发。
	lastAuthCheck time.Time
	// lastPresenceTouch 最近一次 presence 续期时间，用于本地节流。
	lastPresenceTouch time.Time
	// consecutiveDrops 连续投递失败次数（缓冲满）。
	consecutiveDrops int32
	// closeEvent 关闭来源（close/evict/shutdown/slow/auth），用于指标归因。
	closeEvent string
}

// setCloseEvent 记录关闭来源（幂等，保留第一个来源）。
func (c *TCPConnection) setCloseEvent(event string) {
	c.mu.Lock()
	if c.closeEvent == "" {
		c.closeEvent = event
	}
	c.mu.Unlock()
}

// tcpMaxFrameBytes 单帧（一行）上行大小上限（1 MiB），与 WS 的单帧上限保持一致。
// bufio.Scanner 默认会无限扩张缓冲，超大行会直接把内存打满；设置上限后
// 超限行会以 bufio.ErrTooLong 终止扫描并断开连接。
const tcpMaxFrameBytes = 1 << 20

type Handler struct {
	manager    *ConnectionManager
	unified    *gateway.UnifiedConnectionManager
	jwtManager *jwt.JWTManager
	revoker    *auth.Revoker
	eventBus   *types.EventBus
	presence   *gateway.PresenceTracker
	ctx        context.Context
	cancel     context.CancelFunc
}

func NewHandler(mgr *ConnectionManager) *Handler {
	ctx, cancel := context.WithCancel(context.Background())
	redisCache := cache.NewRedisCache()
	eventBus := types.GetEventBus()
	h := &Handler{
		manager:    mgr,
		unified:    gateway.GetUnifiedConnectionManager(),
		jwtManager: jwt.NewJWTManager(),
		revoker:    auth.NewRevoker(redisCache),
		eventBus:   eventBus,
		presence:   gateway.NewPresenceTracker(gateway.NewRedisPresenceStore(redisCache), eventBus),
		ctx:        ctx,
		cancel:     cancel,
	}
	// Kafka 消费统一由 websocket.Handler 持有（同一进程内只有一个消费组），
	// TCP 只负责连接管理与上行转发，避免同一批 topic 被两个消费组重复消费、重复下行。
	return h
}

func (h *Handler) ServeTCP(conn net.Conn) {
	tcpConn := &TCPConnection{
		Conn:      conn,
		Send:      make(chan []byte, 256),
		IsClosed:  false,
		done:      make(chan struct{}),
		createdAt: time.Now(),
		limiter:   gateway.NewInboundTokenBucket(),
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

	conn.Conn.SetReadDeadline(time.Now().Add(gateway.TCPReadTimeout))
	scanner := newFrameScanner(conn.Conn)

	for {
		if !scanner.Scan() {
			// 扫描结束必须区分「正常断开 / 读超时 / 单帧超限」：
			// 原先静默 break 会让超大帧与协议异常完全不可观测。
			logScanError(scanner.Err(), conn.UserID, conn.SessionID)
			break
		}
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		conn.Conn.SetReadDeadline(time.Now().Add(gateway.TCPReadTimeout))

		// 限流在解析之前：高频帧不应消耗 json.Unmarshal 与后续 Kafka 生产资源。
		if !conn.limiter.Allow() {
			gateway.NoteInboundRateLimit("tcp", conn.SessionID, conn.UserID)
			h.sendError(conn, "", gateway.CodeTooManyRequests, "上行消息过于频繁，请稍后重试")
			continue
		}

		var msg IncomingMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			h.sendError(conn, "", gateway.CodeBadRequest, "消息格式无效")
			continue
		}

		h.handleMessage(conn, &msg)
	}
}

// newFrameScanner 构造按行分帧的上行扫描器，单帧上限 tcpMaxFrameBytes。
// 抽成函数是为了让单测能直接验证缓冲上限，而不必拉起完整连接。
func newFrameScanner(r io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), tcpMaxFrameBytes)
	return scanner
}

// logScanError 把扫描终止原因分级记录：
//   - nil：对端正常关闭（无需日志）；
//   - ErrTooLong：单帧超过上限，属协议违规/攻击信号，Warn 并带上用户与会话；
//   - 读超时：空闲连接正常回收，Debug；
//   - 其它：Warn，带上错误。
func logScanError(err error, userID, sessionID string) {
	switch {
	case err == nil:
		return
	case errors.Is(err, bufio.ErrTooLong):
		logger.Warn("TCP 上行帧超过大小限制，断开连接",
			logger.StringField("user_id", userID),
			logger.StringField("session_id", sessionID),
			logger.IntField("limit_bytes", tcpMaxFrameBytes))
	case errors.Is(err, os.ErrDeadlineExceeded):
		logger.Debug("TCP 读取超时，断开空闲连接",
			logger.StringField("user_id", userID),
			logger.StringField("session_id", sessionID))
	default:
		logger.Warn("TCP 读取帧失败，断开连接",
			logger.StringField("user_id", userID),
			logger.StringField("session_id", sessionID),
			logger.ErrorField(err))
	}
}

// maxFlushFramesOnClose 关闭前最多再写出的帧数。
// 上限存在的意义：对端已经读不动时，无界 flush 会让 writePump 在最坏情况下
// 停留「剩余帧数 × 写超时」，把 5s 的停机窗口吃光。
const maxFlushFramesOnClose = 32

// writeTCPFrame 写出单行帧并返回是否成功；调用方保证串行（只有 writePump 会调用）。
func writeTCPFrame(conn *TCPConnection, data []byte) bool {
	conn.mu.Lock()
	defer conn.mu.Unlock()
	if err := conn.Conn.SetWriteDeadline(time.Now().Add(gateway.TCPWriteTimeout)); err != nil {
		return false
	}
	_, err := conn.Conn.Write(append(data, '\n'))
	return err == nil
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
		case <-conn.done:
			// 关闭前先尽力把队列里已有的帧写出去（最多 maxFlushFramesOnClose 帧）。
			// 典型场景：令牌复核失败时「401 错误帧 + cleanup」是同一步完成，
			// 若不 flush，客户端只能看到一个裸 EOF，无法区分「令牌失效」与「网络断了」。
			for i := 0; i < maxFlushFramesOnClose; i++ {
				select {
				case data := <-conn.Send:
					if !writeTCPFrame(conn, data) {
						return
					}
				default:
					return
				}
			}
			return
		case data := <-conn.Send:
			if !writeTCPFrame(conn, data) {
				return
			}
		}
	}
}

// shutdownDrainTimeout TCP 停机的排空窗口。比进程级停机窗口短：
// TCP 侧不需要等待对端确认（直接关 socket 即可），排空只是为了给 writePump
// 一个把 401/断连回执写出去的短暂机会。
const shutdownDrainTimeout = 2 * time.Second

// Shutdown 优雅停机：注销所有会话并强制关闭底层连接，返回仍未退出的连接数。
//
// 与 WS 不同，TCP 没有应用层关闭帧，客户端能感知的只有连接关闭，
// 因此这里不做「发送 GoingAway」的动作，只保证：
//  1. 每条连接都从 manager/unified/presence 中摘除（否则新节点看到的在线状态是脏的）；
//  2. 底层 socket 立即关闭，让阻塞在 Read 的 readPump 立刻返回，进程不挂住。
func (h *Handler) Shutdown(ctx context.Context) int {
	conns := h.manager.AllConnections()
	for _, conn := range conns {
		conn.setCloseEvent("shutdown")
		h.cleanup(conn)
		// cleanup 只标记与注销，不关 socket（由 writePump 负责），
		// 这里主动关闭是为了打断阻塞中的 Read/Write。
		if conn.Conn != nil {
			_ = conn.Conn.Close()
		}
	}

	wait := time.NewTimer(shutdownDrainTimeout)
	defer wait.Stop()

	for {
		remaining := h.manager.Len()
		if remaining == 0 {
			return 0
		}
		select {
		case <-ctx.Done():
			logger.Warn("TCP 停机排空超时，仍有连接未退出",
				logger.IntField("remaining", remaining))
			return remaining
		case <-wait.C:
			// 兜底：即使 manager 未清空也不再阻塞进程退出，剩余的由 OS 回收。
			return h.manager.Len()
		case <-time.After(shutdownPollInterval):
		}
	}
}

// shutdownPollInterval 排空轮询间隔，与 WS 侧一致。
const shutdownPollInterval = 20 * time.Millisecond

func (h *Handler) handleMessage(conn *TCPConnection, msg *IncomingMessage) {
	// request_id 是公共字段（ACK 关联 + 幂等落库），统一在分发前校验，
	// 与 WS 入口共用 gateway.ValidateRequestID 的同一套规则。
	if v := gateway.ValidateRequestID(msg.RequestID); v != nil {
		h.sendError(conn, msg.RequestID, gateway.CodeBadRequest, v.Error())
		return
	}

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
		h.sendError(conn, msg.RequestID, gateway.CodeBadRequest, "未知消息类型")
	}
}

func (h *Handler) handleConnect(conn *TCPConnection, msg *IncomingMessage) {
	var payload ConnectPayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		h.sendError(conn, msg.RequestID, gateway.CodeBadRequest, "载荷格式无效")
		return
	}

	claims, err := h.jwtManager.ParseToken(payload.Token)
	if err != nil {
		obs.RecordGatewayAuthFailure(h.ctx, "tcp", string(gateway.AuthFailureInvalid))
		h.sendError(conn, msg.RequestID, gateway.CodeUnauthorized, "令牌无效")
		return
	}
	state, checkErr := h.revoker.Check(h.ctx, claims)
	obs.RecordGatewayRevocationCheck(h.ctx, "tcp", state.String())
	if checkErr != nil {
		logger.Warn("TCP 建连时撤销查询失败，按 fail-open 放行",
			logger.StringField("user_id", claims.UserID),
			logger.ErrorField(checkErr))
	}
	if state == auth.RevocationRevoked {
		obs.RecordGatewayAuthFailure(h.ctx, "tcp", string(gateway.AuthFailureRevoked))
		h.sendError(conn, msg.RequestID, gateway.CodeUnauthorized, "令牌已失效")
		return
	}

	sessionID := uuid.New().String()
	conn.UserID = claims.UserID
	conn.DeviceID = payload.DeviceID
	conn.SessionID = sessionID
	conn.claims = claims

	obs.RecordGatewayConnection(h.ctx, "tcp", "open")
	h.evictConnection(h.manager.AddConnection(sessionID, conn))
	// 扇出回调复用统一的投递策略：丢弃计数、采样日志、阈值驱逐，
	// 与 WS 侧语义一致（不再出现「TCP 满缓冲直接丢日志、WS 满缓冲计数并驱逐」的双标）。
	h.unified.Register(sessionID, conn.UserID, conn.DeviceID, "tcp", func(data []byte) {
		pushToTCP(conn, data)
	})

	// 只有该用户全网首个会话才发 online：多设备/多节点登录不会重复广播
	h.presence.Online(h.ctx, conn.UserID, conn.DeviceID, sessionID)

	logger.Info("TCP 已连接",
		logger.StringField("user_id", conn.UserID),
		logger.StringField("session_id", sessionID))

	h.sendMessage(conn, downlink.NewRequest(MessageTypeConnect, msg.RequestID,
		ConnectResponsePayload{SessionID: sessionID}))
}

func (h *Handler) handleDisconnect(conn *TCPConnection, msg *IncomingMessage) {
	// 先回执再清理：cleanup 会关闭 done 让 writePump 退出，之后再发送就无法送达
	h.sendMessage(conn, downlink.NewRequest(MessageTypeDisconnect, msg.RequestID, struct{}{}))
	h.cleanup(conn)
}

func (h *Handler) handleHeartbeat(conn *TCPConnection, msg *IncomingMessage) {
	// 心跳是在线会话的续期信号，避免存活连接被当作过期会话剔除。
	// 与 WS 一致走本地节流：客户端心跳频率通常远高于续期所需频率。
	now := time.Now()
	if gateway.ShouldTouchPresence(conn.lastPresenceTouch, now, gateway.PresenceTouchInterval) {
		conn.lastPresenceTouch = now
		h.presence.Touch(h.ctx, conn.UserID, conn.SessionID)
	}

	// TCP 没有服务端定时器（不像 WS 的 ping ticker），客户端心跳就是天然的
	// 「周期性时点」：借它做令牌复核，不需要为每条连接额外起一个 goroutine。
	// 复核失败时先入队一帧 401，再走 cleanup；writePump 退出前会 flush 队列，
	// 保证客户端能收到「为什么断开」而不是一个裸的 EOF。
	if !h.recheckAuth(conn) {
		return
	}

	h.sendMessage(conn, downlink.NewRequest(MessageTypeHeartbeat, msg.RequestID,
		HeartbeatPayload{Timestamp: time.Now().UnixMilli()}))
}

// recheckAuth 复核长连接令牌。返回 false 表示连接已判定失效并完成清理，
// 调用方应立即返回。
func (h *Handler) recheckAuth(conn *TCPConnection) bool {
	now := time.Now()
	if conn.UserID == "" {
		return true
	}
	if !gateway.AuthRecheckDue(conn.lastAuthCheck, now, gateway.AuthRecheckInterval) {
		return true
	}
	conn.lastAuthCheck = now

	state, checkErr := h.revoker.Check(h.ctx, conn.claims)
	obs.RecordGatewayRevocationCheck(h.ctx, "tcp", state.String())
	if checkErr != nil {
		// 与 WS 一致：查询失败 fail-open，用 unknown 指标告警而不是踢掉全部连接。
		logger.Warn("TCP 令牌复核查询失败，按 fail-open 放行",
			logger.StringField("user_id", conn.UserID),
			logger.ErrorField(checkErr))
	}
	reason := gateway.ClassifyAuthFailure(conn.claims, state == auth.RevocationRevoked, now)
	if reason == gateway.AuthFailureNone {
		return true
	}

	obs.RecordGatewayAuthFailure(h.ctx, "tcp", string(reason))
	logger.Warn("TCP 长连接令牌复核失败，主动断开",
		logger.StringField("user_id", conn.UserID),
		logger.StringField("session_id", conn.SessionID),
		logger.StringField("reason", string(reason)))

	conn.setCloseEvent("auth")
	h.sendError(conn, "", gateway.CodeUnauthorized, "令牌已失效，请重新登录")
	h.cleanup(conn)
	return false
}

func (h *Handler) handleChatMessageSend(conn *TCPConnection, msg *IncomingMessage) {
	if conn.UserID == "" {
		h.sendError(conn, msg.RequestID, gateway.CodeUnauthorized, "未连接")
		return
	}

	var payload MessagePayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		h.sendError(conn, msg.RequestID, gateway.CodeBadRequest, "载荷格式无效")
		return
	}

	// 与 WS 入口调用同一批校验函数：chat_id/内容/长度规则只有一份实现。
	if v := gateway.ValidateChatID(payload.ChatID); v != nil {
		h.sendError(conn, msg.RequestID, gateway.CodeBadRequest, v.Error())
		return
	}
	if v := gateway.ValidateContent(payload.Content); v != nil {
		h.sendError(conn, msg.RequestID, gateway.CodeBadRequest, v.Error())
		return
	}
	if v := gateway.ValidateMentionIDs(payload.MentionIDs); v != nil {
		h.sendError(conn, msg.RequestID, gateway.CodeBadRequest, v.Error())
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

	// 与 WS 入口一致：携带幂等键时派生确定性消息 ID，重发不产生重复消息
	messageID := uuid.New().String()
	if msg.RequestID != "" {
		messageID = gateway.DeriveMessageID(conn.UserID, msg.RequestID)
	}
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
	event.RequestID = msg.RequestID

	if err := h.eventBus.PublishMessageEvent(h.ctx, event); err != nil {
		h.sendError(conn, msg.RequestID, gateway.CodeInternal, "发送消息失败")
		return
	}

	h.sendMessage(conn, downlink.NewRequest(MessageTypeAck, msg.RequestID,
		AckPayload{MessageID: messageID}))
}

func (h *Handler) handleTyping(conn *TCPConnection, msg *IncomingMessage) {
	if conn.UserID == "" {
		h.sendError(conn, msg.RequestID, gateway.CodeUnauthorized, "未连接")
		return
	}

	var payload TypingPayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		h.sendError(conn, msg.RequestID, gateway.CodeBadRequest, "载荷格式无效")
		return
	}
	if v := gateway.ValidateChatID(payload.ChatID); v != nil {
		h.sendError(conn, msg.RequestID, gateway.CodeBadRequest, v.Error())
		return
	}

	event := &types.TypingEvent{
		EventType: types.EventTypeTyping,
		UserID:    conn.UserID,
		ChatID:    payload.ChatID,
		IsTyping:  payload.Typing,
		Timestamp: time.Now(),
	}

	if err := h.eventBus.PublishTypingEvent(h.ctx, event); err != nil {
		h.sendError(conn, msg.RequestID, gateway.CodeInternal, "发送输入状态失败")
		return
	}

	h.sendMessage(conn, downlink.NewRequest(MessageTypeAck, msg.RequestID, AckPayload{}))
}

func (h *Handler) handleReadReceipt(conn *TCPConnection, msg *IncomingMessage) {
	if conn.UserID == "" {
		h.sendError(conn, msg.RequestID, gateway.CodeUnauthorized, "未连接")
		return
	}

	var payload ReadReceiptPayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		h.sendError(conn, msg.RequestID, gateway.CodeBadRequest, "载荷格式无效")
		return
	}
	// TCP 侧此前完全不校验已读列表：空 chat_id、空消息 ID、上千条 ID 都会
	// 直接进入 Kafka。这里与 WS 共用同一套规则补齐。
	if v := gateway.ValidateChatID(payload.ChatID); v != nil {
		h.sendError(conn, msg.RequestID, gateway.CodeBadRequest, v.Error())
		return
	}
	if v := gateway.ValidateMessageIDs(payload.MessageIDs); v != nil {
		h.sendError(conn, msg.RequestID, gateway.CodeBadRequest, v.Error())
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
		h.sendError(conn, msg.RequestID, gateway.CodeInternal, "发送已读回执失败")
		return
	}

	h.sendMessage(conn, downlink.NewRequest(MessageTypeAck, msg.RequestID, AckPayload{}))
}

func (h *Handler) sendMessage(conn *TCPConnection, msg OutgoingMessage) {
	data, err := msg.Marshal()
	if err != nil {
		return
	}
	// 满缓冲不再立即 cleanup：瞬时拥塞丢帧即可，连续丢弃达阈值才驱逐。
	pushToTCP(conn, data)
}

func (h *Handler) sendError(conn *TCPConnection, requestID string, code int32, message string) {
	h.sendMessage(conn, downlink.NewRequest(MessageTypeError, requestID,
		ErrorPayload{Code: code, Message: gateway.ErrorMessage(code, message)}))
}

// evictConnection 清理被「单用户连接数上限」淘汰的旧会话，走正常 cleanup 流程。
func (h *Handler) evictConnection(old *TCPConnection) {
	if old == nil {
		return
	}
	obs.RecordGatewayConnection(h.ctx, "tcp", "evict")
	logger.Warn("TCP 单用户连接数超限，淘汰最旧会话",
		logger.StringField("user_id", old.UserID),
		logger.StringField("session_id", old.SessionID),
		logger.IntField("max_connections", maxConnectionsPerUser))
	old.setCloseEvent("evict")
	h.cleanup(old)
}

func (h *Handler) cleanup(conn *TCPConnection) {
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

	if conn.SessionID != "" {
		obs.RecordGatewayConnection(h.ctx, "tcp", event)
		h.manager.RemoveConnection(conn.SessionID)
		h.unified.Unregister(conn.SessionID)
	}

	// 只有该用户全网最后一个会话断开才发 offline：关掉手机端不会让桌面端变离线
	h.presence.Offline(h.ctx, conn.UserID, conn.DeviceID, conn.SessionID)

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
