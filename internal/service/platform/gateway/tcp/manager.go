package tcp

import (
	"sync"

	"Logos/pkg/logger"
)

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

func (m *ConnectionManager) AddConnection(sessionID string, conn *TCPConnection) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.connections[sessionID] = conn

	if _, ok := m.userSessions[conn.UserID]; !ok {
		m.userSessions[conn.UserID] = make(map[string]struct{})
	}
	m.userSessions[conn.UserID][sessionID] = struct{}{}

	logger.Info("TCP: 连接已注册",
		logger.StringField("session_id", sessionID),
		logger.StringField("user_id", conn.UserID))
}

func (m *ConnectionManager) RemoveConnection(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

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
