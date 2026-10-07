package gateway

import (
	"context"
	"sync"
	"time"

	"Logos/pkg/logger"
)

// 大群扇出降级参数。
// 当一次投递的目标用户数超过 largeFanoutThreshold 时，认为进入了「超大群/全员广播」场景：
// 单次同步扇出会给本地发送循环、Redis 大包、远端节点遍历带来瞬时压力，
// 因此降级为「本地同步投递 + 远端分批异步限流发布」。
const (
	// largeFanoutThreshold 触发降级的目标用户数阈值
	largeFanoutThreshold = 500
	// fanoutBatchSize 降级后单次远端发布的用户数上限（限制 Redis 单包体积）
	fanoutBatchSize = 200
	// fanoutBatchInterval 降级后相邻两批远端发布的间隔（限流，避免 Redis/Kafka 风暴）
	fanoutBatchInterval = 50 * time.Millisecond
	// maxConcurrentFanout 同时在跑的降级发布任务上限（背压保护）
	maxConcurrentFanout = 8
)

type SendFunc func(data []byte)

type UnifiedConnection struct {
	UserID    string
	DeviceID  string
	SessionID string
	Protocol  string
	Send      SendFunc
}

type UnifiedConnectionManager struct {
	mu           sync.RWMutex
	connections  map[string]*UnifiedConnection
	userSessions map[string]map[string]bool
	broadcaster  *Broadcaster // 注入的跨节点广播器，nil 时退化为纯本地投递
}

var (
	unifiedMgr  *UnifiedConnectionManager
	unifiedOnce sync.Once
)

func GetUnifiedConnectionManager() *UnifiedConnectionManager {
	unifiedOnce.Do(func() {
		unifiedMgr = &UnifiedConnectionManager{
			connections:  make(map[string]*UnifiedConnection),
			userSessions: make(map[string]map[string]bool),
		}
	})
	return unifiedMgr
}

// SetBroadcaster 注入跨节点广播器
//
// 必须在第一次调用 SendToUser / SendToUsers / BroadcastMessage / BroadcastMessageExcept 之前注入
// 否则 broadcaster 始终为 nil，方法退化为纯本地投递
//
// 传入 nil 等同于禁用跨节点广播，向后兼容单节点部署
func (m *UnifiedConnectionManager) SetBroadcaster(b *Broadcaster) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.broadcaster = b
	if b != nil {
		logger.Info("unified manager: broadcaster enabled",
			logger.StringField("node_id", b.NodeID()),
			logger.StringField("channel", b.Channel()))
	} else {
		logger.Info("unified manager: broadcaster disabled (local-only mode)")
	}
}

func (m *UnifiedConnectionManager) Register(sessionID, userID, deviceID, protocol string, send SendFunc) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.connections[sessionID] = &UnifiedConnection{
		UserID:    userID,
		DeviceID:  deviceID,
		SessionID: sessionID,
		Protocol:  protocol,
		Send:      send,
	}

	if m.userSessions[userID] == nil {
		m.userSessions[userID] = make(map[string]bool)
	}
	m.userSessions[userID][sessionID] = true
}

func (m *UnifiedConnectionManager) Unregister(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	conn, exists := m.connections[sessionID]
	if !exists {
		return
	}

	delete(m.connections, sessionID)

	if sessions, exists := m.userSessions[conn.UserID]; exists {
		delete(sessions, sessionID)
		if len(sessions) == 0 {
			delete(m.userSessions, conn.UserID)
		}
	}
}

// ============================================================================
// Local* 系列方法：纯本地投递，不触发跨节点 Publish
// 供 Broadcaster.Subscribe 的 handler 在收到远端消息后调用，避免回环
// ============================================================================

func (m *UnifiedConnectionManager) LocalSendToUser(userID string, data []byte) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	sessions, exists := m.userSessions[userID]
	if !exists {
		return
	}

	for sessionID := range sessions {
		if conn, ok := m.connections[sessionID]; ok {
			conn.Send(data)
		}
	}
}

func (m *UnifiedConnectionManager) LocalSendToSession(sessionID string, data []byte) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if conn, ok := m.connections[sessionID]; ok {
		conn.Send(data)
	}
}

func (m *UnifiedConnectionManager) LocalBroadcastMessage(data []byte) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, conn := range m.connections {
		conn.Send(data)
	}
}

func (m *UnifiedConnectionManager) LocalBroadcastMessageExcept(data []byte, exceptUserID string) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, conn := range m.connections {
		if conn.UserID != exceptUserID {
			conn.Send(data)
		}
	}
}

// ============================================================================
// 公开 Send 方法：本地先投递 + 远端 Publish 双写
// ============================================================================

// SendToUser 投递消息给指定用户的所有会话
// 本地优先（零延迟路径），同时 Publish 到 redis channel 让其他节点投递
func (m *UnifiedConnectionManager) SendToUser(userID string, data []byte) {
	m.LocalSendToUser(userID, data)
	m.publishToRemote(&BroadcastMessage{
		TargetMode:   TargetModeUser,
		TargetUserID: userID,
		Payload:      data,
	})
}

// fanoutSem 限制并发的降级发布任务数，作为背压保护
var fanoutSem = make(chan struct{}, maxConcurrentFanout)

// SendToUsers 投递消息给多个用户
//
// 小规模：本地同步投递 + 单次 Publish 携带 TargetUserIDs，减少 redis 往返。
// 大规模（超大群）：降级为「本地同步投递 + 远端分批异步限流发布」——
//   - 本地仍同步投递给本节点在线用户（离线用户天然 no-op）；
//   - 远端按 fanoutBatchSize 分批、fanoutBatchInterval 限速异步发布，限制单包体积与发布速率；
//   - 并发任务数受 fanoutSem 约束，队列打满时丢弃远端发布并记录告警（离线节点靠拉取补齐），
//     避免在消息洪峰下把网关/Redis 打满。
func (m *UnifiedConnectionManager) SendToUsers(userIDs []string, data []byte) {
	if len(userIDs) == 0 {
		return
	}

	// 本地投递：无论规模都要做，且对离线用户是廉价的 no-op
	for _, userID := range userIDs {
		m.LocalSendToUser(userID, data)
	}

	if len(userIDs) <= largeFanoutThreshold {
		m.publishToRemote(&BroadcastMessage{
			TargetMode:    TargetModeUsers,
			TargetUserIDs: userIDs,
			Payload:       data,
		})
		return
	}

	// 超大群：降级
	logger.Warn("unified manager: large fanout detected, degrading to batched async publish",
		logger.IntField("fanout_users", len(userIDs)),
		logger.IntField("batch_size", fanoutBatchSize))

	select {
	case fanoutSem <- struct{}{}:
		go func() {
			defer func() { <-fanoutSem }()
			m.publishBatchedToRemote(userIDs, data)
		}()
	default:
		// 背压打满：丢弃远端发布（本地已投递），避免拖垮本节点
		logger.Warn("unified manager: fanout queue saturated, remote publish dropped",
			logger.IntField("fanout_users", len(userIDs)))
	}
}

// publishBatchedToRemote 将超大目标列表分批、限速地发布到远端。
func (m *UnifiedConnectionManager) publishBatchedToRemote(userIDs []string, data []byte) {
	ticker := time.NewTicker(fanoutBatchInterval)
	defer ticker.Stop()

	for i := 0; i < len(userIDs); i += fanoutBatchSize {
		end := i + fanoutBatchSize
		if end > len(userIDs) {
			end = len(userIDs)
		}
		m.publishToRemote(&BroadcastMessage{
			TargetMode:    TargetModeUsers,
			TargetUserIDs: userIDs[i:end],
			Payload:       data,
		})
		if end < len(userIDs) {
			<-ticker.C
		}
	}
}

// BroadcastMessage 广播给所有连接
func (m *UnifiedConnectionManager) BroadcastMessage(data []byte) {
	m.LocalBroadcastMessage(data)
	m.publishToRemote(&BroadcastMessage{
		TargetMode: TargetModeBroadcast,
		Payload:    data,
	})
}

// BroadcastMessageExcept 广播给所有连接，排除指定用户
func (m *UnifiedConnectionManager) BroadcastMessageExcept(data []byte, exceptUserID string) {
	m.LocalBroadcastMessageExcept(data, exceptUserID)
	m.publishToRemote(&BroadcastMessage{
		TargetMode:   TargetModeBroadcast,
		ExceptUserID: exceptUserID,
		Payload:      data,
	})
}

// publishToRemote 发布消息到 redis channel
// broadcaster 为 nil 时为 no-op，向后兼容单节点部署
// 使用 context.Background() 因为：消息推送不应被请求级 ctx 取消
func (m *UnifiedConnectionManager) publishToRemote(msg *BroadcastMessage) {
	m.mu.RLock()
	b := m.broadcaster
	m.mu.RUnlock()

	if b == nil {
		return
	}
	msg.SourceNodeID = b.NodeID()
	if err := b.Publish(context.Background(), msg); err != nil {
		logger.Warn("unified manager: publish to remote failed",
			logger.ErrorField(err),
			logger.StringField("target_mode", msg.TargetMode))
	}
}

// HandleRemoteMessage 处理从远端节点收到的广播消息
// 在 Broadcaster.Subscribe 的 handler 中调用，按 TargetMode 路由到对应 Local* 方法
func (m *UnifiedConnectionManager) HandleRemoteMessage(msg *BroadcastMessage) {
	switch msg.TargetMode {
	case TargetModeUser:
		m.LocalSendToUser(msg.TargetUserID, msg.Payload)
	case TargetModeSession:
		m.LocalSendToSession(msg.TargetSessionID, msg.Payload)
	case TargetModeUsers:
		for _, uid := range msg.TargetUserIDs {
			m.LocalSendToUser(uid, msg.Payload)
		}
	case TargetModeBroadcast:
		m.LocalBroadcastMessageExcept(msg.Payload, msg.ExceptUserID)
	default:
		logger.Warn("unified manager: unknown target mode",
			logger.StringField("target_mode", msg.TargetMode))
	}
}
