package tcp

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"Logos/internal/service/platform/gateway"
	"Logos/pkg/auth"
	"Logos/pkg/jwt"

	jwtlib "github.com/golang-jwt/jwt/v5"
)

func newTestTCPConn(sid string) *TCPConnection {
	return &TCPConnection{
		UserID:    "u1",
		SessionID: sid,
		Send:      make(chan []byte, 1),
		done:      make(chan struct{}),
	}
}

func testTCPHandler(mgr *ConnectionManager) *Handler {
	return &Handler{
		manager: mgr,
		unified: gateway.GetUnifiedConnectionManager(),
		revoker: auth.NewRevoker(nil),
		ctx:     context.Background(),
	}
}

// TestPushToTCP_DropsWhenFull 验证缓冲满时只累计丢弃、不阻塞调用方。
func TestPushToTCP_DropsWhenFull(t *testing.T) {
	conn := newTestTCPConn("s1")

	pushToTCP(conn, []byte("first"))
	if got := <-conn.Send; string(got) != "first" {
		t.Fatalf("首帧应入缓冲，实际 %q", got)
	}

	pushToTCP(conn, []byte("blocker"))
	pushToTCP(conn, []byte("dropped"))

	if n := atomic.LoadInt32(&conn.consecutiveDrops); n != 1 {
		t.Fatalf("缓冲满时应累计 1 次丢弃，实际 %d", n)
	}
	if conn.IsClosed {
		t.Fatal("单次丢弃不应关闭连接（与旧实现的关键差异）")
	}
}

// TestPushToTCP_ResetsDropCounter 验证一次成功投递后计数归零。
func TestPushToTCP_ResetsDropCounter(t *testing.T) {
	conn := newTestTCPConn("s2")

	pushToTCP(conn, []byte("fill"))
	pushToTCP(conn, []byte("drop-1"))
	if n := atomic.LoadInt32(&conn.consecutiveDrops); n != 1 {
		t.Fatalf("期望累计 1 次丢弃，实际 %d", n)
	}

	<-conn.Send
	pushToTCP(conn, []byte("ok"))
	if n := atomic.LoadInt32(&conn.consecutiveDrops); n != 0 {
		t.Fatalf("投递成功后计数应清零，实际 %d", n)
	}
}

// TestPushToTCP_EvictsSlowClient 验证连续丢弃达阈值后驱逐慢客户端。
func TestPushToTCP_EvictsSlowClient(t *testing.T) {
	conn := newTestTCPConn("s3")
	pushToTCP(conn, []byte("fill"))

	before := atomic.LoadInt64(&slowClientEvictions)
	for i := 0; i < maxConsecutiveDrops; i++ {
		pushToTCP(conn, []byte("slow"))
	}

	if got := atomic.LoadInt64(&slowClientEvictions) - before; got != 1 {
		t.Fatalf("连续丢弃达 %d 次应驱逐一次，实际 %d", maxConsecutiveDrops, got)
	}
	if conn.closeEvent != "slow" {
		t.Fatalf("慢客户端驱逐的 closeEvent 应为 slow，实际 %q", conn.closeEvent)
	}
}

// TestPushToTCP_SkipsClosedConnection 验证已关闭连接不再投递也不再计数。
func TestPushToTCP_SkipsClosedConnection(t *testing.T) {
	conn := newTestTCPConn("s4")
	conn.IsClosed = true

	pushToTCP(conn, []byte("ignored"))

	if n := atomic.LoadInt32(&conn.consecutiveDrops); n != 0 {
		t.Fatalf("已关闭连接不应计数，实际 %d", n)
	}
	select {
	case <-conn.Send:
		t.Fatal("已关闭连接不应有帧入缓冲")
	default:
	}
}

func expiredClaims() *jwt.Claims {
	return &jwt.Claims{
		UserID: "u1",
		RegisteredClaims: jwtlib.RegisteredClaims{
			ExpiresAt: jwtlib.NewNumericDate(time.Now().Add(-time.Minute)),
		},
	}
}

// TestTCPRecheckAuthClosesExpiredToken 验证心跳时点复核到过期令牌后主动清理连接。
func TestTCPRecheckAuthClosesExpiredToken(t *testing.T) {
	mgr := NewConnectionManager()
	h := testTCPHandler(mgr)

	conn := newTestTCPConn("s1")
	conn.claims = expiredClaims()
	conn.lastAuthCheck = time.Now().Add(-time.Hour)
	conn.createdAt = time.Now()
	mgr.AddConnection("s1", conn)

	if ok := h.recheckAuth(conn); ok {
		t.Fatal("过期令牌必须复核失败")
	}
	if !conn.IsClosed {
		t.Fatal("复核失败后连接应被清理")
	}
	if conn.closeEvent != "auth" {
		t.Fatalf("closeEvent 应为 auth，实际 %q", conn.closeEvent)
	}
	if n := mgr.Len(); n != 0 {
		t.Fatalf("复核失败后会话应被注销，实际剩余 %d", n)
	}
	// 401 错误帧应先入队，writePump 退出前 flush 出去
	select {
	case frame := <-conn.Send:
		if len(frame) == 0 {
			t.Fatal("401 帧不应为空")
		}
	default:
		t.Fatal("复核失败应先入队 401 错误帧，让客户端知道断开原因")
	}
}

// TestTCPShutdownDrainsConnections 验证停机注销全部会话并返回剩余数 0。
func TestTCPShutdownDrainsConnections(t *testing.T) {
	mgr := NewConnectionManager()
	h := testTCPHandler(mgr)

	for i, sid := range []string{"s1", "s2", "s3"} {
		conn := newTestTCPConn(sid)
		conn.createdAt = time.Now().Add(time.Duration(i) * time.Second)
		mgr.AddConnection(sid, conn)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if remaining := h.Shutdown(ctx); remaining != 0 {
		t.Fatalf("停机后应无剩余连接，实际 %d", remaining)
	}
	if n := mgr.Len(); n != 0 {
		t.Fatalf("停机后 manager 应为空，实际 %d", n)
	}
}

// TestTCPShutdownTimeoutReturnsRemaining 验证 ctx 到期时如实返回剩余连接数。
func TestTCPShutdownTimeoutReturnsRemaining(t *testing.T) {
	mgr := NewConnectionManager()
	h := testTCPHandler(mgr)

	stuck := &TCPConnection{
		UserID: "u1",
		Send:   make(chan []byte, 1),
		done:   make(chan struct{}),
	}
	mgr.AddConnection("s1", stuck)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if remaining := h.Shutdown(ctx); remaining != 1 {
		t.Fatalf("应报告 1 条未退出连接，实际 %d", remaining)
	}
}
