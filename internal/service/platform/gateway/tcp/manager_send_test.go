package tcp

import (
	"sync/atomic"
	"testing"
	"time"
)

func newSendTestTCPConn(userID, sessionID string, buf int) *TCPConnection {
	return &TCPConnection{
		UserID:    userID,
		SessionID: sessionID,
		Send:      make(chan []byte, buf),
		done:      make(chan struct{}),
		createdAt: time.Now(),
	}
}

// TestSendMessageToSession_SkipsClosed 旧实现先判 IsClosed 再发送，
// 中间连接被关闭时仍会写进 Send；统一走 pushToTCP 后判断与发送在同一入口完成。
func TestSendMessageToSession_SkipsClosed(t *testing.T) {
	m := NewConnectionManager()
	conn := newSendTestTCPConn("u1", "s1", 1)
	conn.IsClosed = true
	m.AddConnection("s1", conn)

	m.SendMessageToSession("s1", []byte("x"))

	select {
	case <-conn.Send:
		t.Fatal("已关闭的连接不应收到下行帧")
	default:
	}
}

// TestSendMessageToUser_CountsDropsOnFullBuffer 拥塞必须计入连续丢弃计数。
func TestSendMessageToUser_CountsDropsOnFullBuffer(t *testing.T) {
	m := NewConnectionManager()
	conn := newSendTestTCPConn("u1", "s1", 1)
	conn.Send <- []byte("fill")
	m.AddConnection("s1", conn)

	m.SendMessageToUser("u1", []byte("dropped"))

	if got := atomic.LoadInt32(&conn.consecutiveDrops); got != 1 {
		t.Fatalf("consecutiveDrops = %d, want 1", got)
	}
}

// TestBroadcastMessageExcept_SkipsAnonymousAndSender 系统连接（UserID 为空）
// 不参与广播，exceptUserID 也不回给触发者。
func TestBroadcastMessageExcept_SkipsAnonymousAndSender(t *testing.T) {
	m := NewConnectionManager()
	a := newSendTestTCPConn("u1", "s1", 1)
	b := newSendTestTCPConn("u2", "s2", 1)
	sys := newSendTestTCPConn("", "s3", 1)
	m.AddConnection("s1", a)
	m.AddConnection("s2", b)
	m.AddConnection("s3", sys)

	m.BroadcastMessageExcept([]byte("x"), "u1")

	if len(b.Send) != 1 {
		t.Fatal("其他用户应收到广播")
	}
	if len(a.Send) != 0 {
		t.Fatal("exceptUserID 不应收到广播")
	}
	if len(sys.Send) != 0 {
		t.Fatal("无用户身份的连接不应参与广播")
	}
}
