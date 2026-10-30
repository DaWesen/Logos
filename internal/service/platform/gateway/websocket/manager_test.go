package websocket

import (
	"fmt"
	"testing"
	"time"
)

// TestConnectionManagerEvictsOldestSession 验证单用户连接数达上限后，
// 新连接会淘汰最旧会话，避免重连遗留的会话无限堆积。
func TestConnectionManagerEvictsOldestSession(t *testing.T) {
	mgr := NewConnectionManager()
	base := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

	for i := 0; i < maxConnectionsPerUser; i++ {
		sid := fmt.Sprintf("s%d", i)
		conn := &Connection{
			UserID:    "u1",
			SessionID: sid,
			Send:      make(chan []byte, 1),
			createdAt: base.Add(time.Duration(i) * time.Second),
		}
		if old := mgr.AddConnection(sid, conn); old != nil {
			t.Fatalf("未达上限时不应淘汰会话，实际淘汰 %s", old.SessionID)
		}
	}

	newest := &Connection{
		UserID:    "u1",
		SessionID: "s-new",
		Send:      make(chan []byte, 1),
		createdAt: base.Add(10 * time.Second),
	}
	evicted := mgr.AddConnection("s-new", newest)
	if evicted == nil {
		t.Fatalf("达到上限(%d)后应淘汰最旧会话", maxConnectionsPerUser)
	}
	if evicted.SessionID != "s0" {
		t.Fatalf("应淘汰最旧的 s0，实际淘汰 %s", evicted.SessionID)
	}
	if _, ok := mgr.GetConnection("s0"); ok {
		t.Fatal("被淘汰会话应从 manager 中移除")
	}
	if got := len(mgr.GetUserConnections("u1")); got != maxConnectionsPerUser {
		t.Fatalf("用户会话数应稳定在上限 %d，实际 %d", maxConnectionsPerUser, got)
	}
}

// TestConnectionManagerEvictsPerUser 验证上限按用户隔离，一个用户连接多不会挤掉别人。
func TestConnectionManagerEvictsPerUser(t *testing.T) {
	mgr := NewConnectionManager()
	base := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

	for i := 0; i < maxConnectionsPerUser; i++ {
		sid := fmt.Sprintf("u1-s%d", i)
		mgr.AddConnection(sid, &Connection{
			UserID:    "u1",
			SessionID: sid,
			Send:      make(chan []byte, 1),
			createdAt: base.Add(time.Duration(i) * time.Second),
		})
	}

	// 另一个用户的首个连接绝不应触发淘汰
	if old := mgr.AddConnection("u2-s1", &Connection{
		UserID:    "u2",
		SessionID: "u2-s1",
		Send:      make(chan []byte, 1),
		createdAt: base,
	}); old != nil {
		t.Fatalf("不同用户的连接不应被淘汰，实际淘汰 %s", old.SessionID)
	}

	if got := len(mgr.GetUserConnections("u1")); got != maxConnectionsPerUser {
		t.Fatalf("u1 会话数不应受影响，实际 %d", got)
	}
	if got := len(mgr.GetUserConnections("u2")); got != 1 {
		t.Fatalf("u2 应有 1 条会话，实际 %d", got)
	}
}

// TestConnectionManagerRemoveCleansBothMaps 验证删除会话会同步清空用户索引，
// 避免「connections 已删、userSessions 残留」导致 GetAll 拿到幽灵会话。
func TestConnectionManagerRemoveCleansBothMaps(t *testing.T) {
	mgr := NewConnectionManager()
	mgr.AddConnection("s1", &Connection{UserID: "u1", SessionID: "s1", Send: make(chan []byte, 1)})
	mgr.RemoveConnection("s1")

	if _, ok := mgr.GetConnection("s1"); ok {
		t.Fatal("会话应从 connections 中删除")
	}
	if got := mgr.GetUserConnections("u1"); len(got) != 0 {
		t.Fatalf("用户索引应同步清空，实际 %d 条", len(got))
	}
}
