package tcp

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"Logos/pkg/logger"

	"go.uber.org/zap"
)

// TestMain 注入 nop logger：manager 的注册日志与扫描错误分级都会走 logger，
// 不注入会去读仓库根目录的配置文件。
func TestMain(m *testing.M) {
	logger.SetLogger(zap.NewNop())
	os.Exit(m.Run())
}

// TestFrameScannerRejectsOversizedLine 验证单帧超过 1 MiB 时扫描器以 ErrTooLong 终止，
// 而不是不断扩张缓冲把内存打满。
func TestFrameScannerRejectsOversizedLine(t *testing.T) {
	oversized := strings.Repeat("a", tcpMaxFrameBytes+1024)
	scanner := newFrameScanner(strings.NewReader(oversized))

	if scanner.Scan() {
		t.Fatal("超限帧不应被扫描成功")
	}
	if err := scanner.Err(); !errors.Is(err, bufio.ErrTooLong) {
		t.Fatalf("期望 bufio.ErrTooLong，实际 %v", err)
	}
}

// TestFrameScannerReadsNormalLine 验证上限内的行仍可正常读取，避免限制误伤业务帧。
func TestFrameScannerReadsNormalLine(t *testing.T) {
	line := strings.Repeat("b", 64*1024)
	scanner := newFrameScanner(strings.NewReader(line + "\n"))

	if !scanner.Scan() {
		t.Fatalf("正常行应扫描成功，err=%v", scanner.Err())
	}
	if got := scanner.Text(); got != line {
		t.Fatalf("读到的行被截断: len=%d want=%d", len(got), len(line))
	}
}

// TestFrameScannerJustBelowLimitAccepted 验证接近上限的帧不被误判为超限。
// bufio.Scanner 的 maxTokenSize 是排他上界（含换行符的读取方式下恰好等于上限会报
// ErrTooLong），因此这里取上限 - 1 作为「合法最大帧」。
func TestFrameScannerJustBelowLimitAccepted(t *testing.T) {
	exact := strings.Repeat("c", tcpMaxFrameBytes-1)
	scanner := newFrameScanner(strings.NewReader(exact + "\n"))

	if !scanner.Scan() {
		t.Fatalf("上限内的帧应被接受，err=%v", scanner.Err())
	}
}

// TestManagerEvictsOldestSession 验证 TCP 侧单用户连接数上限语义与 WS 一致。
func TestManagerEvictsOldestSession(t *testing.T) {
	mgr := NewConnectionManager()
	base := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

	for i := 0; i < maxConnectionsPerUser; i++ {
		sid := fmt.Sprintf("s%d", i)
		conn := &TCPConnection{
			UserID:    "u1",
			SessionID: sid,
			Send:      make(chan []byte, 1),
			createdAt: base.Add(time.Duration(i) * time.Second),
		}
		if old := mgr.AddConnection(sid, conn); old != nil {
			t.Fatalf("未达上限时不应淘汰会话，实际淘汰 %s", old.SessionID)
		}
	}

	evicted := mgr.AddConnection("s-new", &TCPConnection{
		UserID:    "u1",
		SessionID: "s-new",
		Send:      make(chan []byte, 1),
		createdAt: base.Add(10 * time.Second),
	})
	if evicted == nil || evicted.SessionID != "s0" {
		t.Fatalf("应淘汰最旧的 s0，实际 %v", evicted)
	}
	if got := len(mgr.GetUserConnections("u1")); got != maxConnectionsPerUser {
		t.Fatalf("用户会话数应稳定在上限 %d，实际 %d", maxConnectionsPerUser, got)
	}
}
