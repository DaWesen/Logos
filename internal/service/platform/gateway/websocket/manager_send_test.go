package websocket

import (
	"sync/atomic"
	"testing"
	"time"
)

func newSendTestConn(userID, sessionID string, buf int) *Connection {
	return &Connection{
		UserID:    userID,
		SessionID: sessionID,
		Send:      make(chan []byte, buf),
		done:      make(chan struct{}),
		createdAt: time.Now(),
	}
}

// TestSendMessageToSession_SkipsClosed 已关闭连接不能再收到帧。
// 旧实现直接 select 到 Send，完全没有 IsClosed 判断（Send 永不关闭，
// 所以不会 panic，但会把消息投给一条已经断开的连接，白占缓冲）。
func TestSendMessageToSession_SkipsClosed(t *testing.T) {
	m := NewConnectionManager()
	conn := newSendTestConn("u1", "s1", 1)
	conn.IsClosed = true
	m.AddConnection("s1", conn)

	m.SendMessageToSession("s1", []byte("x"))

	select {
	case <-conn.Send:
		t.Fatal("已关闭的连接不应收到下行帧")
	default:
	}
}

// TestBroadcastMessageExcept_ExcludesSender 广播不回给触发者。
func TestBroadcastMessageExcept_ExcludesSender(t *testing.T) {
	m := NewConnectionManager()
	a := newSendTestConn("u1", "s1", 1)
	b := newSendTestConn("u2", "s2", 1)
	m.AddConnection("s1", a)
	m.AddConnection("s2", b)

	m.BroadcastMessageExcept([]byte("x"), "u1")

	if len(b.Send) != 1 {
		t.Fatal("其他用户应收到广播")
	}
	if len(a.Send) != 0 {
		t.Fatal("exceptUserID 不应收到广播")
	}
}

// TestSendMessageToUser_DropCountsOnFullBuffer 缓冲满必须走 pushToConn 的计数路径，
// 否则「某个客户端一直在丢帧」永远无法被告警发现。
func TestSendMessageToUser_DropCountsOnFullBuffer(t *testing.T) {
	m := NewConnectionManager()
	conn := newSendTestConn("u1", "s1", 1)
	conn.Send <- []byte("fill")
	m.AddConnection("s1", conn)

	m.SendMessageToUser("u1", []byte("dropped"))

	if got := atomic.LoadInt32(&conn.consecutiveDrops); got != 1 {
		t.Fatalf("consecutiveDrops = %d, want 1", got)
	}
}
