package gateway

import (
	"sync"
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

func (m *UnifiedConnectionManager) SendToUser(userID string, data []byte) {
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

func (m *UnifiedConnectionManager) SendToUsers(userIDs []string, data []byte) {
	for _, userID := range userIDs {
		m.SendToUser(userID, data)
	}
}

func (m *UnifiedConnectionManager) BroadcastMessage(data []byte) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, conn := range m.connections {
		conn.Send(data)
	}
}

func (m *UnifiedConnectionManager) BroadcastMessageExcept(data []byte, exceptUserID string) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, conn := range m.connections {
		if conn.UserID != exceptUserID {
			conn.Send(data)
		}
	}
}
