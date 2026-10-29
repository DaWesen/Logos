package handler

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"Logos/internal/service/messaging/im/service"
	"Logos/pkg/auth"
	pb "Logos/proto_gen/im"

	"google.golang.org/grpc/metadata"
)

// TestMain 切换到仓库根目录：handler 里的日志依赖 ./config 配置初始化。
func TestMain(m *testing.M) {
	if wd, err := os.Getwd(); err == nil {
		for dir := wd; ; dir = filepath.Dir(dir) {
			if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
				_ = os.Chdir(dir)
				break
			}
			if filepath.Dir(dir) == dir {
				break
			}
		}
	}
	os.Exit(m.Run())
}

type stubIMService struct {
	connectedUserID  string
	connectedSession string
	disconnected     string
	heartbeat        string
}

func (s *stubIMService) Connect(_ context.Context, userID, deviceID, sessionID string) error {
	s.connectedUserID = userID
	s.connectedSession = sessionID
	return nil
}

func (s *stubIMService) Disconnect(_ context.Context, sessionID string) error {
	s.disconnected = sessionID
	return nil
}

func (s *stubIMService) GetOnlineStatus(context.Context, []string) (map[string]bool, error) {
	return nil, nil
}

func (s *stubIMService) SetOnlineStatus(context.Context, string, bool) error { return nil }

func (s *stubIMService) Heartbeat(_ context.Context, sessionID string) error {
	s.heartbeat = sessionID
	return nil
}

func (s *stubIMService) SendTypingStatus(context.Context, string, string, bool) error { return nil }

func (s *stubIMService) BroadcastMessage(context.Context, string) error { return nil }

func (s *stubIMService) StartPresenceConsumer() error { return nil }

var _ service.IMService = (*stubIMService)(nil)

func authedContext(userID string) context.Context {
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs(auth.UserIDKey, userID))
}

func TestConnectUsesAuthenticatedIdentity(t *testing.T) {
	stub := &stubIMService{}
	h := NewIMServiceImpl(stub)

	resp, err := h.Connect(authedContext("1001"), &pb.ConnectRequest{UserId: "9999", DeviceId: "dev-1"})
	if err != nil {
		t.Fatalf("不应返回 gRPC 错误: %v", err)
	}
	if resp.Code != 200 {
		t.Fatalf("期望 200，实际 %d (%s)", resp.Code, resp.Message)
	}
	if stub.connectedUserID != "1001" || stub.connectedSession != "session_1001" || resp.SessionId != "session_1001" {
		t.Fatalf("身份未以认证信息为准: user=%q session=%q resp=%q",
			stub.connectedUserID, stub.connectedSession, resp.SessionId)
	}
}

func TestConnectRequiresAuth(t *testing.T) {
	h := NewIMServiceImpl(&stubIMService{})

	resp, err := h.Connect(context.Background(), &pb.ConnectRequest{UserId: "1001"})
	if err != nil {
		t.Fatalf("不应返回 gRPC 错误: %v", err)
	}
	if resp.Code != 401 {
		t.Fatalf("期望 401，实际 %d", resp.Code)
	}
}

func TestSessionScopedOperations(t *testing.T) {
	ctx := authedContext("1001")

	t.Run("心跳 本人会话放行", func(t *testing.T) {
		stub := &stubIMService{}
		h := NewIMServiceImpl(stub)
		resp, _ := h.Heartbeat(ctx, &pb.HeartbeatRequest{SessionId: "session_1001"})
		if resp.Code != 200 || stub.heartbeat != "session_1001" {
			t.Fatalf("期望放行并转发心跳，实际 %d / %q", resp.Code, stub.heartbeat)
		}
	})

	t.Run("心跳 他人会话拒绝", func(t *testing.T) {
		stub := &stubIMService{}
		h := NewIMServiceImpl(stub)
		resp, _ := h.Heartbeat(ctx, &pb.HeartbeatRequest{SessionId: "session_1002"})
		if resp.Code != 403 || stub.heartbeat != "" {
			t.Fatalf("期望 403 且不转发，实际 %d / %q", resp.Code, stub.heartbeat)
		}
	})

	t.Run("断开 本人会话放行", func(t *testing.T) {
		stub := &stubIMService{}
		h := NewIMServiceImpl(stub)
		resp, _ := h.Disconnect(ctx, &pb.DisconnectRequest{SessionId: "session_1001"})
		if resp.Code != 200 || stub.disconnected != "session_1001" {
			t.Fatalf("期望放行并断开，实际 %d / %q", resp.Code, stub.disconnected)
		}
	})

	t.Run("断开 他人会话拒绝", func(t *testing.T) {
		stub := &stubIMService{}
		h := NewIMServiceImpl(stub)
		resp, _ := h.Disconnect(ctx, &pb.DisconnectRequest{SessionId: "session_1002"})
		if resp.Code != 403 || stub.disconnected != "" {
			t.Fatalf("期望 403 且不执行断开，实际 %d / %q", resp.Code, stub.disconnected)
		}
	})

	t.Run("非法会话格式拒绝", func(t *testing.T) {
		h := NewIMServiceImpl(&stubIMService{})
		resp, _ := h.Disconnect(ctx, &pb.DisconnectRequest{SessionId: "1001"})
		if resp.Code != 403 {
			t.Fatalf("期望 403，实际 %d", resp.Code)
		}
	})
}

func TestSetOnlineStatusRequiresAuth(t *testing.T) {
	h := NewIMServiceImpl(&stubIMService{})

	// 缺失 metadata 时必须返回 401 而不是 panic。
	resp, err := h.SetOnlineStatus(context.Background(), &pb.SetOnlineStatusRequest{})
	if err != nil {
		t.Fatalf("不应返回 gRPC 错误: %v", err)
	}
	if resp.Code != 401 {
		t.Fatalf("期望 401，实际 %d", resp.Code)
	}
}
