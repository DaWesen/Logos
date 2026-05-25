package websocket

import (
	"sync"

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

func (m *ConnectionManager) AddConnection(sessionID string, conn *Connection) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.connections[sessionID] = conn

	if m.userSessions[conn.UserID] == nil {
		m.userSessions[conn.UserID] = make(map[string]bool)
	}
	m.userSessions[conn.UserID][sessionID] = true
}

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

		// 不在这儿关闭，让 cleanup 去关闭，避免重复关闭
	}
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
