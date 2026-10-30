package websocket

import (
	"os"
	"sync/atomic"
	"testing"

	"Logos/pkg/logger"

	"go.uber.org/zap"
)

// TestMain 注入 nop logger，避免测试依赖配置文件
func TestMain(m *testing.M) {
	logger.SetLogger(zap.NewNop())
	os.Exit(m.Run())
}

// TestPushToConn_FillsBufferThenDrops 验证正常投递不丢帧，缓冲满后只记丢弃不阻塞。
func TestPushToConn_FillsBufferThenDrops(t *testing.T) {
	conn := &Connection{Send: make(chan []byte, 1), SessionID: "s1"}

	pushToConn(conn, []byte("first"))
	if got := <-conn.Send; string(got) != "first" {
		t.Fatalf("首帧应入缓冲，实际 %q", got)
	}

	// 重新填满缓冲
	pushToConn(conn, []byte("blocker"))
	pushToConn(conn, []byte("dropped"))

	if n := atomic.LoadInt32(&conn.consecutiveDrops); n != 1 {
		t.Fatalf("缓冲满时应累计 1 次丢弃，实际 %d", n)
	}
}

// TestPushToConn_ResetsDropCounterOnSuccess 验证一次成功投递后连续丢弃计数归零。
func TestPushToConn_ResetsDropCounterOnSuccess(t *testing.T) {
	conn := &Connection{Send: make(chan []byte, 1), SessionID: "s2"}

	pushToConn(conn, []byte("fill"))
	pushToConn(conn, []byte("drop-1"))
	if n := atomic.LoadInt32(&conn.consecutiveDrops); n != 1 {
		t.Fatalf("期望累计 1 次丢弃，实际 %d", n)
	}

	<-conn.Send // 客户端读走一帧
	pushToConn(conn, []byte("ok"))
	if n := atomic.LoadInt32(&conn.consecutiveDrops); n != 0 {
		t.Fatalf("投递成功后计数应清零，实际 %d", n)
	}
}

// TestPushToConn_EvictsSlowClient 验证连续丢弃达到阈值后主动断开连接。
func TestPushToConn_EvictsSlowClient(t *testing.T) {
	conn := &Connection{Send: make(chan []byte, 1), SessionID: "s3", UserID: "u3"}
	pushToConn(conn, []byte("fill")) // 占满缓冲，后续全部丢弃

	before := atomic.LoadInt64(&slowClientEvictions)
	for i := 0; i < maxConsecutiveDrops; i++ {
		pushToConn(conn, []byte("slow"))
	}

	if got := atomic.LoadInt64(&slowClientEvictions) - before; got != 1 {
		t.Fatalf("连续丢弃达 %d 次应断开一次慢客户端，实际断开 %d 次", maxConsecutiveDrops, got)
	}
	if n := atomic.LoadInt32(&conn.consecutiveDrops); n != maxConsecutiveDrops {
		t.Fatalf("丢弃计数应为 %d，实际 %d", maxConsecutiveDrops, n)
	}
}

// TestPushToConn_SkipsClosedConnection 验证已关闭连接不再投递、也不计入丢弃。
func TestPushToConn_SkipsClosedConnection(t *testing.T) {
	conn := &Connection{Send: make(chan []byte, 1), SessionID: "s4"}
	conn.IsClosed = true

	pushToConn(conn, []byte("ignored"))

	if n := atomic.LoadInt32(&conn.consecutiveDrops); n != 0 {
		t.Fatalf("已关闭连接不应计入丢弃，实际 %d", n)
	}
	select {
	case <-conn.Send:
		t.Fatal("已关闭连接不应有帧入缓冲")
	default:
	}
}
