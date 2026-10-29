package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"Logos/pkg/auth"
	pbChat "Logos/proto_gen/chat"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// withUserID 必须基于请求上下文：客户端断开时下游调用要一起被取消，
// 同时保留认证中间件写入的调用方身份 metadata。
func TestWithUserID_UsesRequestContext(t *testing.T) {
	gin.SetMode(gin.TestMode)

	reqCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req := httptest.NewRequest("POST", "/", nil)
	req = req.WithContext(reqCtx)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req
	c.Set("user_id", "user-1")

	h := &Handler{}
	got := h.withUserID(c)

	md, ok := metadata.FromOutgoingContext(got)
	if !ok {
		t.Fatal("下游 ctx 缺少 outgoing metadata")
	}
	if values := md.Get(auth.UserIDKey); len(values) == 0 || values[0] != "user-1" {
		t.Fatalf("user_id metadata = %v, want user-1", values)
	}

	// 请求取消后，下游上下文必须同步取消（而不是挂在 Background 上继续执行）
	cancel()
	select {
	case <-got.Done():
	case <-time.After(time.Second):
		t.Fatal("请求取消后下游 ctx 未被取消")
	}
}

// fakeMuteChatClient 只实现 MuteGroupMember，记录下发的 pb 请求用于断言。
type fakeMuteChatClient struct {
	pbChat.ChatServiceClient
	got *pbChat.MuteGroupMemberRequest
}

func (f *fakeMuteChatClient) MuteGroupMember(_ context.Context, req *pbChat.MuteGroupMemberRequest, _ ...grpc.CallOption) (*pbChat.MuteGroupMemberResponse, error) {
	f.got = req
	return &pbChat.MuteGroupMemberResponse{Code: 200, Message: "设置成功"}, nil
}

// TestMuteGroupMember_ConvertsMinutesToMuteType 覆盖网关侧 DTO 的换算：
// 前端传「分钟数」，pb 需要枚举 + 绝对时间；缺参或负数必须在网关层拒绝。
func TestMuteGroupMember_ConvertsMinutesToMuteType(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name       string
		body       string
		wantStatus int
		wantType   pbChat.MuteType
		wantUntil  bool
	}{
		{"临时禁言 30 分钟", `{"group_id":"g1","user_id":"u1","duration":30}`, http.StatusOK, pbChat.MuteType_MUTE_TYPE_TEMPORARY, true},
		{"0 表示永久禁言", `{"group_id":"g1","user_id":"u1","duration":0}`, http.StatusOK, pbChat.MuteType_MUTE_TYPE_PERMANENT, false},
		{"缺 duration 拒绝", `{"group_id":"g1","user_id":"u1"}`, http.StatusBadRequest, pbChat.MuteType_MUTE_TYPE_UNSPECIFIED, false},
		{"负数拒绝", `{"group_id":"g1","user_id":"u1","duration":-1}`, http.StatusBadRequest, pbChat.MuteType_MUTE_TYPE_UNSPECIFIED, false},
		{"超过 1 年拒绝", `{"group_id":"g1","user_id":"u1","duration":525601}`, http.StatusBadRequest, pbChat.MuteType_MUTE_TYPE_UNSPECIFIED, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := &fakeMuteChatClient{}
			h := &Handler{ChatClient: client}

			req := httptest.NewRequest(http.MethodPost, "/api/v1/chat/group/mute", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = req

			before := time.Now()
			h.MuteGroupMember(c)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d, body = %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantStatus != http.StatusOK {
				if client.got != nil {
					t.Fatal("请求被拒绝时不应调用下游 gRPC")
				}
				return
			}
			if client.got == nil {
				t.Fatal("未调用下游 MuteGroupMember")
			}
			if client.got.GetMuteType() != tc.wantType {
				t.Fatalf("mute_type = %v, want %v", client.got.GetMuteType(), tc.wantType)
			}
			if tc.wantUntil {
				until := client.got.GetMuteUntil().AsTime()
				want := before.Add(30 * time.Minute)
				if until.Before(want) || until.After(want.Add(time.Minute)) {
					t.Fatalf("mute_until = %v, want ≈ %v", until, want)
				}
			} else if client.got.GetMuteUntil() != nil {
				t.Fatalf("永久禁言不应设置 mute_until: %v", client.got.GetMuteUntil())
			}
		})
	}
}

// TestWriteSSEEvent 序列化失败必须返回错误（调用方据此结束流、取消上游生成），成功则写出标准 SSE 帧。
func TestWriteSSEEvent(t *testing.T) {
	t.Run("成功写出帧", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		if err := writeSSEEvent(c, map[string]interface{}{"content": "hi", "done": false}); err != nil {
			t.Fatalf("写出 SSE 失败: %v", err)
		}
		body := rec.Body.String()
		if !strings.HasPrefix(body, "data: ") || !strings.HasSuffix(body, "\n\n") || !strings.Contains(body, `"content":"hi"`) {
			t.Fatalf("SSE 帧格式不符: %q", body)
		}
	})

	t.Run("不可序列化返回错误", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		if err := writeSSEEvent(c, map[string]interface{}{"bad": make(chan int)}); err == nil {
			t.Fatal("chan 无法序列化，应返回错误让调用方结束流")
		}
	})
}
