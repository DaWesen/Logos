package websocket

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestInboundFrameLimitRejectsOversizedFrame 验证超过 1MiB 的上行帧会被
// gorilla 以 ErrReadLimit 拒绝并断开，而不是整帧读进内存交给业务解析。
func TestInboundFrameLimitRejectsOversizedFrame(t *testing.T) {
	testUpgrader := websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin:     func(*http.Request) bool { return true },
	}

	serverErr := make(chan error, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := testUpgrader.Upgrade(w, r, nil)
		if err != nil {
			serverErr <- err
			return
		}
		defer c.Close()

		applyInboundFrameLimit(c)
		_, _, err = c.ReadMessage()
		serverErr <- err
	}))
	defer srv.Close()

	dialer := websocket.Dialer{}
	client, _, err := dialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatalf("客户端连接失败: %v", err)
	}
	defer client.Close()

	oversized := make([]byte, inboundFrameLimit+1024)
	for i := range oversized {
		oversized[i] = 'a'
	}
	// 服务端可能在客户端写满之前就判定超限并关闭，写侧错误可以忽略。
	_ = client.WriteMessage(websocket.TextMessage, oversized)

	select {
	case err := <-serverErr:
		if !errors.Is(err, websocket.ErrReadLimit) {
			t.Fatalf("期望 ErrReadLimit，实际 %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("服务端未在 5s 内因超限断开")
	}
}

// TestInboundFrameLimitAcceptsNormalFrame 验证上限内的正常帧仍可读取，
// 避免把限制设得过紧误伤业务消息。
func TestInboundFrameLimitAcceptsNormalFrame(t *testing.T) {
	testUpgrader := websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin:     func(*http.Request) bool { return true },
	}

	got := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := testUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()

		applyInboundFrameLimit(c)
		_, msg, err := c.ReadMessage()
		if err != nil {
			got <- "error: " + err.Error()
			return
		}
		got <- string(msg)
	}))
	defer srv.Close()

	dialer := websocket.Dialer{}
	client, _, err := dialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatalf("客户端连接失败: %v", err)
	}
	defer client.Close()

	payload := strings.Repeat("b", 64*1024)
	if err := client.WriteMessage(websocket.TextMessage, []byte(payload)); err != nil {
		t.Fatalf("写入正常帧失败: %v", err)
	}

	select {
	case msg := <-got:
		if msg != payload {
			t.Fatalf("读到的帧被截断: len=%d want=%d", len(msg), len(payload))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("服务端未在 5s 内读到正常帧")
	}
}
