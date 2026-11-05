package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	pb "Logos/proto_gen/bot"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"
)

// fakeBotClient 只实现 ListBots，记录网关下发的过滤条件。
// calls 用于断言非法枚举没有穿透到下游（否则下游会真的返回全部 Bot）。
type fakeBotClient struct {
	pb.BotServiceClient
	calls int
	got   *pb.ListBotsRequest
}

func (f *fakeBotClient) ListBots(_ context.Context, in *pb.ListBotsRequest, _ ...grpc.CallOption) (*pb.ListBotsResponse, error) {
	f.calls++
	f.got = in
	return &pb.ListBotsResponse{Code: 0, Message: "ok"}, nil
}

func newBotRouter(client pb.BotServiceClient, userID string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := &Handler{BotClient: client}
	r := gin.New()
	r.GET("/bot", func(c *gin.Context) {
		if userID != "" {
			c.Set("user_id", userID)
		}
		h.ListBots(c)
	})
	return r
}

func newBotRouterWithSetup(client pb.BotServiceClient, setup func(*gin.Context)) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := &Handler{BotClient: client}
	r := gin.New()
	r.GET("/bot", func(c *gin.Context) {
		if setup != nil {
			setup(c)
		}
		h.ListBots(c)
	})
	return r
}

// 身份缺失有三种形态：key 不存在、类型不是 string、字符串为空。
// 旧代码只用 `if userID, exists := c.Get("user_id"); exists` 判断，
// 后两种都会把空串透传给下游；而下游 DAO 是 `if userId != ""` 才加过滤，
// 空身份等于不加过滤，ListBots 会返回全库用户的 Bot。
func TestListBots_RejectsMissingOrInvalidIdentity(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*gin.Context)
	}{
		{"缺少 user_id", nil},
		{"user_id 类型不是 string", func(c *gin.Context) { c.Set("user_id", 123) }},
		{"user_id 为空串", func(c *gin.Context) { c.Set("user_id", "") }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := &fakeBotClient{}
			rec := doQuery(t, newBotRouterWithSetup(client, tc.setup), "/bot")

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("HTTP 状态 = %d, want 401, body = %s", rec.Code, rec.Body.String())
			}
			if client.calls != 0 {
				t.Fatalf("身份不可信时不应调用下游，实际调用 %d 次", client.calls)
			}
		})
	}
}

// 字符串枚举拼错时必须 400。旧实现把未命中的值一律当 UNSPECIFIED，
// `?type=bulitin` 于是变成「不过滤」，接口 200 返回全部 Bot，
// 调用方以为筛了内置 Bot，实际拿到全部。
func TestListBots_RejectsUnknownEnumValues(t *testing.T) {
	cases := []string{
		"/bot?type=bulitin",
		"/bot?type=Builtin",
		"/bot?type=1",
		"/bot?status=actve",
		"/bot?status=ACTIVE",
		"/bot?status=1",
	}

	for _, path := range cases {
		t.Run(path, func(t *testing.T) {
			client := &fakeBotClient{}
			rec := doQuery(t, newBotRouter(client, "user-1"), path)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("HTTP 状态 = %d, want 400, body = %s", rec.Code, rec.Body.String())
			}
			if client.calls != 0 {
				t.Fatalf("非法枚举不应调用下游，实际调用 %d 次", client.calls)
			}
		})
	}
}

// 合法值必须原样映射到 proto 枚举，缺省仍是 UNSPECIFIED（不过滤）。
func TestListBots_MapsEnumNames(t *testing.T) {
	cases := []struct {
		query    string
		wantType pb.BotType
		wantStat pb.BotStatus
	}{
		{"", pb.BotType_BOT_TYPE_UNSPECIFIED, pb.BotStatus_BOT_STATUS_UNSPECIFIED},
		{"?type=builtin", pb.BotType_BOT_TYPE_BUILTIN, pb.BotStatus_BOT_STATUS_UNSPECIFIED},
		{"?type=custom", pb.BotType_BOT_TYPE_CUSTOM, pb.BotStatus_BOT_STATUS_UNSPECIFIED},
		{"?status=active", pb.BotType_BOT_TYPE_UNSPECIFIED, pb.BotStatus_BOT_STATUS_ACTIVE},
		{"?status=inactive", pb.BotType_BOT_TYPE_UNSPECIFIED, pb.BotStatus_BOT_STATUS_INACTIVE},
		{"?status=deleted", pb.BotType_BOT_TYPE_UNSPECIFIED, pb.BotStatus_BOT_STATUS_DELETED},
		{"?type=builtin&status=active", pb.BotType_BOT_TYPE_BUILTIN, pb.BotStatus_BOT_STATUS_ACTIVE},
	}

	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			client := &fakeBotClient{}
			rec := doQuery(t, newBotRouter(client, "user-1"), "/bot"+tc.query)

			if rec.Code != http.StatusOK {
				t.Fatalf("HTTP 状态 = %d, want 200, body = %s", rec.Code, rec.Body.String())
			}
			if client.got.GetType() != tc.wantType {
				t.Fatalf("type = %v, want %v", client.got.GetType(), tc.wantType)
			}
			if client.got.GetStatus() != tc.wantStat {
				t.Fatalf("status = %v, want %v", client.got.GetStatus(), tc.wantStat)
			}
			// 身份只能来自认证中间件，不能从 query 读。
			if client.got.GetUserId() != "user-1" {
				t.Fatalf("user_id = %q, want user-1", client.got.GetUserId())
			}
		})
	}
}

// 分页非法值在枚举校验之前就应被拒，避免「先 400 枚举再 400 分页」的重复解析。
func TestListBots_RejectsBadPagination(t *testing.T) {
	client := &fakeBotClient{}
	rec := doQuery(t, newBotRouter(client, "user-1"), "/bot?page=-1")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("HTTP 状态 = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}
	if client.calls != 0 {
		t.Fatalf("非法分页不应调用下游，实际调用 %d 次", client.calls)
	}
}

// 下游不可用时保持 503，不要把 nil client 变成 panic。
func TestListBots_NoClientReturns503(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/bot", nil)
	h := &Handler{}
	c, _ := gin.CreateTestContext(rec)
	c.Request = req
	h.ListBots(c)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("HTTP 状态 = %d, want 503", rec.Code)
	}
}
