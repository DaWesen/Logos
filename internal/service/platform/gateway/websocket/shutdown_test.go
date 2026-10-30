package websocket

import (
	"context"
	"testing"
	"time"

	"Logos/internal/service/platform/gateway"
	"Logos/pkg/auth"
	"Logos/pkg/jwt"

	jwtlib "github.com/golang-jwt/jwt/v5"
)

// testHandler 构造一个不依赖配置文件/Redis 的 Handler：
// manager 与 unified 是真实实现（停机排空逻辑必须走真实的两张表），
// revoker 用 nil cache（IsRevoked fail-open 返回 false），presence 留空（nil 接收者安全）。
func testHandler() *Handler {
	return &Handler{
		manager: NewConnectionManager(),
		unified: gateway.GetUnifiedConnectionManager(),
		revoker: auth.NewRevoker(nil),
		ctx:     context.Background(),
	}
}

// newTestConn 构造一条只用于 manager 的生命周期连接（无真实 socket）。
func newTestConn(userID, sessionID string, createdAt time.Time) *Connection {
	return &Connection{
		UserID:    userID,
		SessionID: sessionID,
		Send:      make(chan []byte, 8),
		done:      make(chan struct{}),
		createdAt: createdAt,
	}
}

// TestShutdownDrainsAllConnections 验证停机后 manager 清空、返回 0，
// 且不因为缺少 presence（nil）而 panic。
func TestShutdownDrainsAllConnections(t *testing.T) {
	h := testHandler()
	base := time.Now()
	for i, sid := range []string{"s1", "s2", "s3"} {
		conn := newTestConn("u1", sid, base.Add(time.Duration(i)*time.Second))
		h.manager.AddConnection(sid, conn)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if remaining := h.Shutdown(ctx); remaining != 0 {
		t.Fatalf("停机后应无剩余连接，实际 %d", remaining)
	}
	if n := h.manager.Len(); n != 0 {
		t.Fatalf("停机后 manager 应为空，实际 %d", n)
	}
}

// TestShutdownMarksCloseEvent 验证停机路径把关闭来源标记为 shutdown，
// 便于指标区分「发布下线」与「用户主动断开」。
func TestShutdownMarksCloseEvent(t *testing.T) {
	h := testHandler()
	conn := newTestConn("u1", "s1", time.Now())
	h.manager.AddConnection("s1", conn)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	h.Shutdown(ctx)

	if conn.closeEvent != "shutdown" {
		t.Fatalf("停机连接 closeEvent 应为 shutdown，实际 %q", conn.closeEvent)
	}
	if !conn.IsClosed {
		t.Fatal("停机后连接应标记为已关闭")
	}
}

// TestShutdownTimeoutReturnsRemaining 验证 ctx 到期时返回仍未退出的连接数。
// 构造「登记时用的 key 与连接自己的 SessionID 不一致」的连接：cleanup 依据
// SessionID 注销，因此它不会被移除，正好模拟“卡住不退出”的会话。
func TestShutdownTimeoutReturnsRemaining(t *testing.T) {
	h := testHandler()
	stuck := &Connection{
		UserID: "u1",
		Send:   make(chan []byte, 8),
		done:   make(chan struct{}),
	}
	h.manager.AddConnection("s1", stuck)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即到期：排空循环没有机会等到 manager 清空

	if remaining := h.Shutdown(ctx); remaining != 1 {
		t.Fatalf("应报告 1 条未退出连接，实际 %d", remaining)
	}
}

// validClaims 返回一个未过期的令牌声明。
func validClaims(exp time.Time) *jwt.Claims {
	return &jwt.Claims{
		UserID: "u1",
		RegisteredClaims: jwtlib.RegisteredClaims{
			ExpiresAt: jwtlib.NewNumericDate(exp),
		},
	}
}

// TestRecheckAuthPassesForValidToken 验证有效令牌复核通过且不关闭连接。
func TestRecheckAuthPassesForValidToken(t *testing.T) {
	h := testHandler()
	conn := &Connection{
		UserID:        "u1",
		SessionID:     "s1",
		claims:        validClaims(time.Now().Add(time.Hour)),
		lastAuthCheck: time.Now().Add(-time.Hour),
	}

	if ok := h.recheckAuth(conn); !ok {
		t.Fatal("有效令牌应通过复核")
	}
	if conn.IsClosed {
		t.Fatal("有效令牌不应关闭连接")
	}
}

// TestRecheckAuthClosesExpiredToken 验证过期令牌复核失败并标记关闭来源为 auth。
func TestRecheckAuthClosesExpiredToken(t *testing.T) {
	h := testHandler()
	conn := &Connection{
		UserID:        "u1",
		SessionID:     "s1",
		claims:        validClaims(time.Now().Add(-time.Minute)),
		lastAuthCheck: time.Now().Add(-time.Hour),
	}

	if ok := h.recheckAuth(conn); ok {
		t.Fatal("过期令牌必须复核失败")
	}
	if conn.closeEvent != "auth" {
		t.Fatalf("复核失败的 closeEvent 应为 auth，实际 %q", conn.closeEvent)
	}
}

// TestRecheckAuthSkipsWithinInterval 验证未到期时不做复核（避免每个 ping 都打 Redis），
// 表现为「过期令牌在间隔内仍然通过」。
func TestRecheckAuthSkipsWithinInterval(t *testing.T) {
	h := testHandler()
	conn := &Connection{
		UserID:        "u1",
		SessionID:     "s1",
		claims:        validClaims(time.Now().Add(-time.Minute)),
		lastAuthCheck: time.Now(),
	}

	if ok := h.recheckAuth(conn); !ok {
		t.Fatal("复核间隔未到时应直接通过")
	}
}

// TestRecheckAuthSkipsUnauthenticated 验证未认证连接不参与复核（由认证超时负责回收）。
func TestRecheckAuthSkipsUnauthenticated(t *testing.T) {
	h := testHandler()
	conn := &Connection{SessionID: "s1"}

	if ok := h.recheckAuth(conn); !ok {
		t.Fatal("未认证连接不应被复核关闭")
	}
}
