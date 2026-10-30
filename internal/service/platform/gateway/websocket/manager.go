package websocket

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"Logos/internal/service/platform/gateway"
	"Logos/pkg/jwt"
	"Logos/pkg/logger"
	"Logos/pkg/obs"

	"github.com/gorilla/websocket"
)

type Connection struct {
	UserID    string
	DeviceID  string
	SessionID string
	Conn      *websocket.Conn
	Send      chan []byte
	mu        sync.Mutex
	IsClosed  bool
	done      chan struct{} // 关闭信号；Send 永不关闭，避免并发发送到已关闭 channel 导致 panic
	// createdAt 连接建立时间：用户连接数超限时用它选最旧会话淘汰。
	createdAt time.Time
	// limiter 单连接上行令牌桶；nil 表示不限流（仅测试/特殊构造路径）。
	limiter *gateway.TokenBucket
	// consecutiveDrops 连续投递失败次数：缓冲持续满说明客户端已跟不上。
	consecutiveDrops int32
	// claims 建连时解析出的令牌声明。定期复核撤销状态与过期时间都依赖它；
	// 不在连接上保存原始 token 字符串，避免令牌被日志/内存转储意外带出。
	claims *jwt.Claims
	// lastAuthCheck 最近一次令牌复核时间；零值表示「建立连接后尚未复核」。
	// 只在 writePump 的 ping ticker 中读写，无并发写。
	lastAuthCheck time.Time
	// lastPresenceTouch 最近一次 presence 续期时间，用于本地节流（见 touchPresence）。
	// 只在 readPump 的 pong/心跳路径读写，无并发写。
	lastPresenceTouch time.Time
	// writeMu 串行化对底层 socket 的所有写入（writePump 正文、ping、停机关闭帧、
	// 复核失败的错误帧）。gorilla/websocket 允许并发读、禁止并发写，违反会直接
	// panic 或让两条帧交错。
	writeMu sync.Mutex
	// closeEvent 关闭来源：close/evict/shutdown/slow/auth，用于指标区分，
	// 避免把「被淘汰」与「用户主动断开」混成一个计数。
	closeEvent string
}

// setCloseEvent 记录关闭来源（幂等，只保留第一次写入的来源）。
func (c *Connection) setCloseEvent(event string) {
	c.mu.Lock()
	if c.closeEvent == "" {
		c.closeEvent = event
	}
	c.mu.Unlock()
}

// maxConnectionsPerUser 单个用户在同一协议下的最大并发连接数。
//
// 客户端重连逻辑（弱网抖动、进程被杀）经常在旧连接尚未被判超时前建立新连接，
// 旧会话会一直占着 fd、Send 缓冲与 presence 记录，直到 90s 读超时才被回收。
// 手机切后台、前端定时重连这类场景下几条残留会话是常态，若不设上限，
// 一个异常客户端能在几分钟内把单用户会话堆到几十条，扇出时全部重复投递。
// 超出后淘汰最旧会话：新连接总是「当前有效的那个」，旧连接即便还活着也已被取代。
const maxConnectionsPerUser = 5

// maxConsecutiveDrops 连续丢弃上限，超过后主动断开该连接。
const maxConsecutiveDrops = 100

// inboundFrameLimit 单帧上行消息大小上限（1 MiB）。
//
// 没有上限时，恶意/异常客户端可以发一个几百 MB 的文本帧，gorilla 会把整帧读进内存
// 再交给 json.Unmarshal，直接打爆进程（OOM），且这条连接在解析前一直占着内存。
// 正常业务帧（消息、输入状态、已读回执）远小于 1 MiB，超限一律按协议违规断开。
const inboundFrameLimit = 1 << 20

// applyInboundFrameLimit 设置读上限；超限时 gorilla 返回 websocket.ErrReadLimit 并关闭连接。
func applyInboundFrameLimit(conn *websocket.Conn) {
	if conn == nil {
		return
	}
	conn.SetReadLimit(inboundFrameLimit)
}

// slowClientEvictions 因发送缓冲持续满被主动断开的连接数（可观测，测试亦依赖）。
var slowClientEvictions int64

// pushToConn 非阻塞投递下行帧。
//
// 缓冲满时不能阻塞：这正是广播路径（含跨节点 Redis 扇出）的调用点，
// 一个卡死的客户端会拖住整轮扇出。因此选择丢弃本帧，并做两件事：
//  1. 日志按 1/50 采样，避免洪峰下每条丢弃都刷一条日志；
//  2. 连续丢弃超过 maxConsecutiveDrops 则关闭底层连接，让 readPump/writePump
//     退出并走正常清理流程；客户端重连后通过离线同步补齐，比无限堆积更可控。
func pushToConn(conn *Connection, data []byte) {
	if conn == nil || conn.Send == nil {
		return
	}

	conn.mu.Lock()
	if conn.IsClosed {
		conn.mu.Unlock()
		return
	}
	conn.mu.Unlock()

	select {
	case conn.Send <- data:
		atomic.StoreInt32(&conn.consecutiveDrops, 0)
	default:
		// 丢弃本身也要可观测：只打日志的话，洪峰下采样日志会淹在其它日志里，
		// 「某个客户端/某类消息一直在丢」无法被告警发现。
		obs.RecordGatewayDrop(context.Background(), "websocket", "buffer_full")
		n := atomic.AddInt32(&conn.consecutiveDrops, 1)
		if n == 1 || n%50 == 0 {
			logger.Warn("WebSocket 发送通道已满，丢弃下行帧",
				logger.StringField("session_id", conn.SessionID),
				logger.IntField("consecutive_drops", int(n)))
		}
		if n >= maxConsecutiveDrops {
			logger.Warn("WebSocket 慢客户端连续丢弃过多，主动断开",
				logger.StringField("session_id", conn.SessionID),
				logger.StringField("user_id", conn.UserID))
			atomic.AddInt64(&slowClientEvictions, 1)
			// 关闭来源记为 slow：cleanup 会据此打「因慢客户端被淘汰」的指标，
			// 与用户主动断开、停机排空区分开。
			conn.setCloseEvent("slow")
			if conn.Conn != nil {
				_ = conn.Conn.Close()
			}
		}
	}
}

type ConnectionManager struct {
	connections  map[string]*Connection
	userSessions map[string]map[string]bool
	mu           sync.RWMutex
}

func NewConnectionManager() *ConnectionManager {
	return &ConnectionManager{
		connections:  make(map[string]*Connection),
		userSessions: make(map[string]map[string]bool),
	}
}

// AddConnection 登记连接；若该用户连接数已达上限，返回被淘汰的最旧连接。
//
// 返回值交给调用方清理（关闭底层连接、从 unified 注销、发 offline），
// 因为 manager 不持有 unified/presence 依赖，在这里直接关连接会造成循环依赖。
// 淘汰时避免了「先加后删」的瞬时超限：先摘掉最旧会话再登记新会话。
func (m *ConnectionManager) AddConnection(sessionID string, conn *Connection) *Connection {
	m.mu.Lock()
	defer m.mu.Unlock()

	var evicted *Connection
	if sessions, ok := m.userSessions[conn.UserID]; ok && len(sessions) >= maxConnectionsPerUser {
		oldestID := ""
		var oldest *Connection
		for sid := range sessions {
			c := m.connections[sid]
			if c == nil {
				continue
			}
			if oldest == nil || c.createdAt.Before(oldest.createdAt) ||
				(c.createdAt.Equal(oldest.createdAt) && sid < oldestID) {
				oldest, oldestID = c, sid
			}
		}
		if oldest != nil {
			m.removeLocked(oldestID)
			evicted = oldest
		}
	}

	m.connections[sessionID] = conn

	if m.userSessions[conn.UserID] == nil {
		m.userSessions[conn.UserID] = make(map[string]bool)
	}
	m.userSessions[conn.UserID][sessionID] = true

	return evicted
}

func (m *ConnectionManager) RemoveConnection(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.removeLocked(sessionID)
}

// removeLocked 从两张表里摘除会话，调用方必须已持有 m.mu。
func (m *ConnectionManager) removeLocked(sessionID string) {
	conn, exists := m.connections[sessionID]
	if !exists {
		return
	}
	delete(m.connections, sessionID)

	if sessions, ok := m.userSessions[conn.UserID]; ok {
		delete(sessions, sessionID)
		if len(sessions) == 0 {
			delete(m.userSessions, conn.UserID)
		}
	}

	// 不在这儿关闭，让 cleanup 去关闭，避免重复关闭
}

// AllConnections 返回当前全部连接的快照。
//
// 停机排空需要在不持有锁的情况下逐条发关闭帧（写帧可能阻塞到写超时），
// 因此这里复制切片后立刻释放读锁，绝不能让扇出路径等一次网络写。
func (m *ConnectionManager) AllConnections() []*Connection {
	m.mu.RLock()
	defer m.mu.RUnlock()

	conns := make([]*Connection, 0, len(m.connections))
	for _, conn := range m.connections {
		conns = append(conns, conn)
	}
	return conns
}

// Len 返回当前登记连接数（停机排空用它判断是否已清空）。
func (m *ConnectionManager) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.connections)
}

func (m *ConnectionManager) GetConnection(sessionID string) (*Connection, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	conn, exists := m.connections[sessionID]
	return conn, exists
}

func (m *ConnectionManager) GetUserConnections(userID string) []*Connection {
	m.mu.RLock()
	defer m.mu.RUnlock()

	sessions, exists := m.userSessions[userID]
	if !exists {
		return nil
	}

	connections := make([]*Connection, 0, len(sessions))
	for sessionID := range sessions {
		if conn, exists := m.connections[sessionID]; exists {
			connections = append(connections, conn)
		}
	}
	return connections
}

func (m *ConnectionManager) SendMessageToUser(userID string, message []byte) {
	connections := m.GetUserConnections(userID)
	for _, conn := range connections {
		select {
		case conn.Send <- message:
		default:
		}
	}
}

func (m *ConnectionManager) SendMessageToSession(sessionID string, message []byte) {
	if conn, exists := m.GetConnection(sessionID); exists {
		select {
		case conn.Send <- message:
		default:
		}
	}
}

func (m *ConnectionManager) BroadcastMessageExcept(message []byte, exceptUserID string) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, conn := range m.connections {
		if conn.UserID != exceptUserID {
			select {
			case conn.Send <- message:
			default:
			}
		}
	}
}
