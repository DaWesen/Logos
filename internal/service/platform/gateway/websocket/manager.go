package websocket

import (
	"sync"

	"github.com/gorilla/websocket"
)

// Connection 表示单个 WebSocket 连接
type Connection struct {
	UserID    string
	DeviceID  string
	SessionID string
	Conn      *websocket.Conn
	Send      chan []byte
	mu        sync.Mutex
	IsClosed  bool
}

// ConnectionManager 管理所有活跃的 WebSocket 连接
type ConnectionManager struct {
	connections  map[string]*Connection
	userSessions map[string]map[string]bool
	mu           sync.RWMutex
}

// NewConnectionManager 创建新的连接管理器
func NewConnectionManager() *ConnectionManager {
	return &ConnectionManager{
		connections:  make(map[string]*Connection),
		userSessions: make(map[string]map[string]bool),
	}
}

// AddConnection 添加新连接到管理器
func (m *ConnectionManager) AddConnection(sessionID string, conn *Connection) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.connections[sessionID] = conn

	if m.userSessions[conn.UserID] == nil {
		m.userSessions[conn.UserID] = make(map[string]bool)
	}
	m.userSessions[conn.UserID][sessionID] = true
}

// RemoveConnection 从管理器中移除连接
func (m *ConnectionManager) RemoveConnection(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if conn, exists := m.connections[sessionID]; exists {
		delete(m.connections, sessionID)

		if sessions, exists := m.userSessions[conn.UserID]; exists {
			delete(sessions, sessionID)
			if len(sessions) == 0 {
				delete(m.userSessions, conn.UserID)
			}
		}

		close(conn.Send)
	}
}

// GetConnection 通过会话 ID 获取连接
func (m *ConnectionManager) GetConnection(sessionID string) (*Connection, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	conn, exists := m.connections[sessionID]
	return conn, exists
}

// GetUserConnections 获取用户的所有连接
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

// IsUserOnline 检查用户是否在线
func (m *ConnectionManager) IsUserOnline(userID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	_, exists := m.userSessions[userID]
	return exists
}

// BroadcastMessage 向所有连接用户广播消息
func (m *ConnectionManager) BroadcastMessage(message []byte) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, conn := range m.connections {
		select {
		case conn.Send <- message:
		default:
		}
	}
}

// SendMessageToUser 向用户的所有会话发送消息
func (m *ConnectionManager) SendMessageToUser(userID string, message []byte) {
	connections := m.GetUserConnections(userID)
	for _, conn := range connections {
		select {
		case conn.Send <- message:
		default:
		}
	}
}

// SendMessageToSession 向特定会话发送消息
func (m *ConnectionManager) SendMessageToSession(sessionID string, message []byte) {
	if conn, exists := m.GetConnection(sessionID); exists {
		select {
		case conn.Send <- message:
		default:
		}
	}
}

// GetOnlineUsers 获取所有在线用户 ID
func (m *ConnectionManager) GetOnlineUsers() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	users := make([]string, 0, len(m.userSessions))
	for userID := range m.userSessions {
		users = append(users, userID)
	}
	return users
}

// ConnectionCount 获取活跃连接总数
func (m *ConnectionManager) ConnectionCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.connections)
}

// BroadcastMessageExcept 广播消息给除指定用户外的所有在线用户
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
