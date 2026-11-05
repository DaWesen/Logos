package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	pbCommon "Logos/proto_gen/common"
	pbQuestion "Logos/proto_gen/question"
	pbRecommend "Logos/proto_gen/recommend"
	pbUser "Logos/proto_gen/user"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"
)

type fakeQuestionClient struct {
	pbQuestion.QAServiceClient

	calls    int
	history  *pbQuestion.HistoryReq
	recommendReq *pbQuestion.GetRecommendedQuestionsReq
	recommendResp *pbCommon.BaseResp
}

func (f *fakeQuestionClient) GetHistory(_ context.Context, in *pbQuestion.HistoryReq, _ ...grpc.CallOption) (*pbQuestion.HistoryResp, error) {
	f.calls++
	f.history = in
	return &pbQuestion.HistoryResp{BaseResp: &pbCommon.BaseResp{StatusCode: 200}}, nil
}

func (f *fakeQuestionClient) GetRecommendedQuestions(_ context.Context, in *pbQuestion.GetRecommendedQuestionsReq, _ ...grpc.CallOption) (*pbCommon.BaseResp, error) {
	f.calls++
	f.recommendReq = in
	if f.recommendResp != nil {
		return f.recommendResp, nil
	}
	return &pbCommon.BaseResp{StatusCode: 200, StatusMessage: `{"questions":["q1","q2","q3","q4","q5"]}`}, nil
}

type fakeRecommendClient struct {
	pbRecommend.RecommendationServiceClient

	calls   int
	items   *pbRecommend.RecommendationReq
	history *pbRecommend.HistoryReq
}

func (f *fakeRecommendClient) GetRecommendations(_ context.Context, in *pbRecommend.RecommendationReq, _ ...grpc.CallOption) (*pbRecommend.RecommendationResp, error) {
	f.calls++
	f.items = in
	return &pbRecommend.RecommendationResp{BaseResp: &pbCommon.BaseResp{StatusCode: 200}}, nil
}

func (f *fakeRecommendClient) GetRecommendationHistory(_ context.Context, in *pbRecommend.HistoryReq, _ ...grpc.CallOption) (*pbRecommend.HistoryResp, error) {
	f.calls++
	f.history = in
	return &pbRecommend.HistoryResp{BaseResp: &pbCommon.BaseResp{StatusCode: 200}}, nil
}

type fakeUserClient struct {
	pbUser.UserServiceClient

	calls  int
	stats  *pbUser.UserStatsReq
	avatar *pbUser.UpdateAvatarReq
}

func (f *fakeUserClient) GetUserStats(_ context.Context, in *pbUser.UserStatsReq, _ ...grpc.CallOption) (*pbUser.UserStatsResp, error) {
	f.calls++
	f.stats = in
	return &pbUser.UserStatsResp{BaseResp: &pbCommon.BaseResp{StatusCode: 200}}, nil
}

// newIdentityRouter 模拟认证中间件：user_id 为登录用户，未设置时等同未认证。
func newIdentityRouter(userID string, h *Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	api := r.Group("/api/v1")
	api.Use(func(c *gin.Context) {
		if userID != "" {
			c.Set("user_id", userID)
		}
		c.Next()
	})
	api.GET("/qa/history", h.GetHistory)
	api.GET("/qa/recommended", h.GetRecommendedQuestions)
	api.GET("/recommend/items", h.GetRecommendations)
	api.GET("/recommend/history", h.GetRecommendationHistory)
	api.GET("/users/stats", h.GetUserStats)
	return r
}

func getIdentity(t *testing.T, r *gin.Engine, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// IDOR：旧实现直接采信 ?user_id=，任何登录用户改个数字就能读他人的问答历史。
func TestGetHistory_RejectsForeignUserID(t *testing.T) {
	client := &fakeQuestionClient{}
	rec := getIdentity(t, newIdentityRouter("7", &Handler{QuestionClient: client}), "/api/v1/qa/history?user_id=9")

	if rec.Code != http.StatusForbidden {
		t.Fatalf("HTTP 状态 = %d, want 403, body = %s", rec.Code, rec.Body.String())
	}
	if client.calls != 0 {
		t.Fatalf("越权请求不应调用下游，实际调用 %d 次", client.calls)
	}
}

func TestGetHistory_UsesAuthenticatedUser(t *testing.T) {
	client := &fakeQuestionClient{}
	rec := getIdentity(t, newIdentityRouter("7", &Handler{QuestionClient: client}), "/api/v1/qa/history")

	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP 状态 = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	if client.history.GetUserId() != 7 {
		t.Fatalf("下游 user_id = %d, want 7", client.history.GetUserId())
	}
}

// 未认证请求不能退化成「查用户 0」，否则匿名用户也能拿到一份数据。
func TestIdentityEndpoints_RequireAuth(t *testing.T) {
	paths := []string{
		"/api/v1/qa/history",
		"/api/v1/qa/recommended",
		"/api/v1/recommend/items",
		"/api/v1/recommend/history",
		"/api/v1/users/stats",
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			rec := getIdentity(t, newIdentityRouter("", &Handler{
				QuestionClient:  &fakeQuestionClient{},
				RecommendClient: &fakeRecommendClient{},
				UserClient:      &fakeUserClient{},
			}), path)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("HTTP 状态 = %d, want 401, body = %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestRecommendEndpoints_RejectForeignUserID(t *testing.T) {
	for _, path := range []string{
		"/api/v1/recommend/items?user_id=9",
		"/api/v1/recommend/history?user_id=9",
		"/api/v1/users/stats?user_id=9",
	} {
		t.Run(path, func(t *testing.T) {
			questionClient := &fakeQuestionClient{}
			recommendClient := &fakeRecommendClient{}
			userClient := &fakeUserClient{}
			rec := getIdentity(t, newIdentityRouter("7", &Handler{
				QuestionClient:  questionClient,
				RecommendClient: recommendClient,
				UserClient:      userClient,
			}), path)

			if rec.Code != http.StatusForbidden {
				t.Fatalf("HTTP 状态 = %d, want 403, body = %s", rec.Code, rec.Body.String())
			}
			if recommendClient.calls+userClient.calls+questionClient.calls != 0 {
				t.Fatalf("越权请求不应调用下游")
			}
		})
	}
}

// /qa/recommended 没有 :userId 路由参数，旧实现恒为 0；现在必须用登录身份。
func TestGetRecommendedQuestions_UsesAuthenticatedUser(t *testing.T) {
	client := &fakeQuestionClient{}
	rec := getIdentity(t, newIdentityRouter("7", &Handler{QuestionClient: client}), "/api/v1/qa/recommended")

	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP 状态 = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	if client.recommendReq.GetUserId() != 7 {
		t.Fatalf("下游 user_id = %d, want 7", client.recommendReq.GetUserId())
	}
}

// 下游把结果塞在 StatusMessage 的 JSON 里，网关必须解出来放进 data，
// 否则前端拿到 200 但列表恒空（表现为「暂无推荐问题」）。
func TestGetRecommendedQuestions_UnwrapsStatusMessage(t *testing.T) {
	client := &fakeQuestionClient{}
	rec := getIdentity(t, newIdentityRouter("7", &Handler{QuestionClient: client}), "/api/v1/qa/recommended?limit=2")

	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP 状态 = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Data []struct {
			Question string `json:"question"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析响应失败: %v, body = %s", err, rec.Body.String())
	}
	if len(body.Data) != 2 {
		t.Fatalf("data 长度 = %d, want 2（limit 截断），body = %s", len(body.Data), rec.Body.String())
	}
	if body.Data[0].Question != "q1" {
		t.Fatalf("首个问题 = %q, want q1", body.Data[0].Question)
	}
}

// 契约被破坏时返回 502，而不是用空数组伪装成「没有推荐」。
func TestGetRecommendedQuestions_RejectsMalformedPayload(t *testing.T) {
	client := &fakeQuestionClient{
		recommendResp: &pbCommon.BaseResp{StatusCode: 200, StatusMessage: "not-json"},
	}
	rec := getIdentity(t, newIdentityRouter("7", &Handler{QuestionClient: client}), "/api/v1/qa/recommended")

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("HTTP 状态 = %d, want 502, body = %s", rec.Code, rec.Body.String())
	}
}
