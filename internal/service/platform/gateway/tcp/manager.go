package tcp

import (
	"sync"

	"Logos/pkg/logger"
)

// maxConnectionsPerUser 单用户最大并发 TCP 连接数，超出后淘汰最旧会话。
// 与 WS 侧保持同一语义：重连遗留的旧会话不应无限堆积、重复参与扇出。
const maxConnectionsPerUser = 5

type ConnectionManager struct {
	mu           sync.RWMutex
	connections  map[string]*TCPConnection
	userSessions map[string]map[string]struct{}
}

func NewConnectionManager() *ConnectionManager {
	return &ConnectionManager{
		connections:  make(map[string]*TCPConnection),
		userSessions: make(map[string]map[string]struct{}),
	}
}

// AddConnection 登记连接并返回被淘汰的最旧会话（未超限时为 nil）。
// manager 不持有 unified/presence，清理动作交给调用方，避免依赖倒挂。
func (m *ConnectionManager) AddConnection(sessionID string, conn *TCPConnection) *TCPConnection {
	m.mu.Lock()
	defer m.mu.Unlock()

	var evicted *TCPConnection
	if sessions, ok := m.userSessions[conn.UserID]; ok && len(sessions) >= maxConnectionsPerUser {
		oldestID := ""
		var oldest *TCPConnection
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

	if _, ok := m.userSessions[conn.UserID]; !ok {
		m.userSessions[conn.UserID] = make(map[string]struct{})
	}
	m.userSessions[conn.UserID][sessionID] = struct{}{}

	logger.Info("TCP: 连接已注册",
		logger.StringField("session_id", sessionID),
		logger.StringField("user_id", conn.UserID))

	return evicted
}

func (m *ConnectionManager) RemoveConnection(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.removeLocked(sessionID)
}

// removeLocked 从两张表里摘除会话，调用方必须已持有 m.mu。
func (m *ConnectionManager) removeLocked(sessionID string) {
	conn, ok := m.connections[sessionID]
	if !ok {
		return
	}

	delete(m.connections, sessionID)

	if conn != nil && conn.UserID != "" {
		if sessions, ok := m.userSessions[conn.UserID]; ok {
			delete(sessions, sessionID)
			if len(sessions) == 0 {
				delete(m.userSessions, conn.UserID)
			}
		}
	}
}

// AllConnections 返回全部连接的快照（停机排空用，避免持锁时做网络 IO）。
func (m *ConnectionManager) AllConnections() []*TCPConnection {
	m.mu.RLock()
	defer m.mu.RUnlock()

	conns := make([]*TCPConnection, 0, len(m.connections))
	for _, conn := range m.connections {
		conns = append(conns, conn)
	}
	return conns
}

// Len 返回当前登记连接数。
func (m *ConnectionManager) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.connections)
}

func (m *ConnectionManager) GetUserConnections(userID string) []*TCPConnection {
	m.mu.RLock()
	defer m.mu.RUnlock()

	sessions, ok := m.userSessions[userID]
	if !ok {
		return nil
	}

	conns := make([]*TCPConnection, 0, len(sessions))
	for sid := range sessions {
		if conn, ok := m.connections[sid]; ok && !conn.IsClosed {
			conns = append(conns, conn)
		}
	}
	return conns
}

func (m *ConnectionManager) SendMessageToUser(userID string, message []byte) {
	conns := m.GetUserConnections(userID)
	for _, conn := range conns {
		select {
		case conn.Send <- message:
		default:
			logger.Warn("TCP: 用户发送通道已满，跳过",
				logger.StringField("user_id", userID))
		}
	}
}

func (m *ConnectionManager) SendMessageToSession(sessionID string, message []byte) {
	m.mu.RLock()
	conn, ok := m.connections[sessionID]
	m.mu.RUnlock()

	if !ok || conn.IsClosed {
		return
	}

	select {
	case conn.Send <- message:
	default:
	}
}

func (m *ConnectionManager) BroadcastMessageExcept(message []byte, exceptUserID string) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, conn := range m.connections {
		if conn.IsClosed || conn.UserID == "" || conn.UserID == exceptUserID {
			continue
		}
		select {
		case conn.Send <- message:
		default:
		}
	}
}
