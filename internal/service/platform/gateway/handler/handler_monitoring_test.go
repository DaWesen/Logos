package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"Logos/config"

	pbCommon "Logos/proto_gen/common"
	pb "Logos/proto_gen/monitoring"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"
)

// fakeServiceStatusClient 只实现服务状态读取，其余方法沿用内嵌接口的 nil 实现。
// 监控状态是网关唯一「失败后端到端回退本地探测」的读路径，需要能精确构造
// RPC 失败、脏 payload、空列表等分支。
type fakeServiceStatusClient struct {
	pb.MonitoringServiceClient

	listResp  *pbCommon.BaseResp
	listErr   error
	listCalls int

	getResp  *pbCommon.BaseResp
	getErr   error
	getCalls int
}

func (f *fakeServiceStatusClient) ListServiceStatus(_ context.Context, _ *pb.EmptyReq, _ ...grpc.CallOption) (*pbCommon.BaseResp, error) {
	f.listCalls++
	return f.listResp, f.listErr
}

func (f *fakeServiceStatusClient) GetServiceStatus(_ context.Context, _ *pb.GetByServiceNameReq, _ ...grpc.CallOption) (*pbCommon.BaseResp, error) {
	f.getCalls++
	return f.getResp, f.getErr
}

func newMonitoringTestHandler(client pb.MonitoringServiceClient) *Handler {
	// 端口全为 0 时 probeLocalServices 不会发起真实拨号，兜底结果为空列表，
	// 单测因此既不依赖外部服务，也不会因为连接超时变慢。
	return &Handler{MonitoringClient: client, Cfg: &config.Config{}}
}

func runMonitoringHandler(t *testing.T, h func(*gin.Context), target string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, target, nil)
	h(c)

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是合法 JSON: %v, body = %s", err, rec.Body.String())
	}
	return rec, body
}

func mustDataSlice(t *testing.T, body map[string]any) []any {
	t.Helper()
	raw, ok := body["data"]
	if !ok {
		t.Fatalf("响应缺少 data 字段: %v", body)
	}
	// 兜底路径必须给出 []，不能是 null：前端对 data 直接 .map，null 会炸。
	arr, ok := raw.([]any)
	if !ok {
		t.Fatalf("data 类型 = %T, want []any", raw)
	}
	return arr
}

func TestListServiceStatuses_ForwardsMonitoringList(t *testing.T) {
	client := &fakeServiceStatusClient{
		listResp: &pbCommon.BaseResp{
			StatusCode:    0,
			StatusMessage: `{"data":[{"service_name":"logos.chat","status":"UP"},{"service_name":"logos.bot","status":"DOWN"}]}`,
		},
	}
	h := newMonitoringTestHandler(client)

	rec, body := runMonitoringHandler(t, h.ListServiceStatuses, "/api/v1/monitoring/services")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if client.listCalls != 1 {
		t.Fatalf("ListServiceStatus 调用次数 = %d, want 1", client.listCalls)
	}
	// 两个服务都不在本地端口表里（端口全为 0），只做透传，不触发拨号。
	if got := len(mustDataSlice(t, body)); got != 2 {
		t.Fatalf("data 长度 = %d, want 2", got)
	}
}

// TestListServiceStatuses_FallsBackOnBadPayload 覆盖「监控服务可用但契约漂移」的四类输入：
// 解析失败、空数组、缺 data、顶层不是对象。它们都能回退本地探测，但必须留下日志，
// 不能再像旧实现那样把降级伪装成正常空列表。
func TestListServiceStatuses_FallsBackOnBadPayload(t *testing.T) {
	cases := []struct {
		name string
		resp *pbCommon.BaseResp
	}{
		{"非法 JSON", &pbCommon.BaseResp{StatusMessage: `not-json`}},
		{"空数组", &pbCommon.BaseResp{StatusMessage: `{"data":[]}`}},
		{"缺少 data", &pbCommon.BaseResp{StatusMessage: `{"items":[]}`}},
		{"顶层非对象", &pbCommon.BaseResp{StatusMessage: `[{"service_name":"x"}]`}},
		{"空 message", &pbCommon.BaseResp{StatusMessage: ``}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newMonitoringTestHandler(&fakeServiceStatusClient{listResp: tc.resp})
			rec, body := runMonitoringHandler(t, h.ListServiceStatuses, "/api/v1/monitoring/services")

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
			}
			if got := len(mustDataSlice(t, body)); got != 0 {
				t.Fatalf("兜底 data 长度 = %d, want 0", got)
			}
			if body["code"] != float64(http.StatusOK) {
				t.Fatalf("响应体 code = %v, want 200", body["code"])
			}
		})
	}
}

func TestListServiceStatuses_FallsBackWhenRPCFails(t *testing.T) {
	h := newMonitoringTestHandler(&fakeServiceStatusClient{listErr: errors.New("connection refused")})
	rec, body := runMonitoringHandler(t, h.ListServiceStatuses, "/api/v1/monitoring/services")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	if got := len(mustDataSlice(t, body)); got != 0 {
		t.Fatalf("兜底 data 长度 = %d, want 0", got)
	}
}

func TestListServiceStatuses_NilClientFallsBack(t *testing.T) {
	h := newMonitoringTestHandler(nil)
	rec, body := runMonitoringHandler(t, h.ListServiceStatuses, "/api/v1/monitoring/services")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	if got := len(mustDataSlice(t, body)); got != 0 {
		t.Fatalf("兜底 data 长度 = %d, want 0", got)
	}
}

func TestListServiceStatuses_PropagatesBusinessError(t *testing.T) {
	h := newMonitoringTestHandler(&fakeServiceStatusClient{
		listResp: &pbCommon.BaseResp{StatusCode: 500, StatusMessage: "db unavailable"},
	})
	rec, body := runMonitoringHandler(t, h.ListServiceStatuses, "/api/v1/monitoring/services")

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500, body = %s", rec.Code, rec.Body.String())
	}
	if body["message"] != "db unavailable" {
		t.Fatalf("message = %v, want db unavailable", body["message"])
	}
}

func TestListServiceStatuses_KeepsUnrecognizedEntries(t *testing.T) {
	// 契约漂移的条目不能被静默丢弃：保留原始条目，前端至少还能看到它存在。
	client := &fakeServiceStatusClient{
		listResp: &pbCommon.BaseResp{
			StatusMessage: `{"data":[1,"oops",{"status":"UP"},{"service_name":"","status":"UP"},{"service_name":"unknown.svc","status":"UP"}]}`,
		},
	}
	h := newMonitoringTestHandler(client)

	rec, body := runMonitoringHandler(t, h.ListServiceStatuses, "/api/v1/monitoring/services")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := len(mustDataSlice(t, body)); got != 5 {
		t.Fatalf("data 长度 = %d, want 5（不可识别条目也要保留）", got)
	}
}

func TestListServiceStatuses_MergeWithProbeToleratesBadStatusType(t *testing.T) {
	// status 不是字符串时旧实现会直接断言失败或静默跳过；现在只记 Warn 并保留条目。
	client := &fakeServiceStatusClient{
		listResp: &pbCommon.BaseResp{
			StatusMessage: `{"data":[{"service_name":"unknown.svc","status":1}]}`,
		},
	}
	h := newMonitoringTestHandler(client)

	rec, body := runMonitoringHandler(t, h.ListServiceStatuses, "/api/v1/monitoring/services")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	data := mustDataSlice(t, body)
	if len(data) != 1 {
		t.Fatalf("data 长度 = %d, want 1", len(data))
	}
	entry, ok := data[0].(map[string]any)
	if !ok {
		t.Fatalf("条目类型 = %T, want map", data[0])
	}
	if entry["status"] != float64(1) {
		t.Fatalf("status = %v, want 原值 1（不得被改写）", entry["status"])
	}
}

func TestGetServiceStatus_InvalidPayloadIs500(t *testing.T) {
	cases := []struct {
		name string
		resp *pbCommon.BaseResp
	}{
		{"非法 JSON", &pbCommon.BaseResp{StatusMessage: `{"data":`}},
		{"顶层非对象", &pbCommon.BaseResp{StatusMessage: `[1,2,3]`}},
		{"空 message", &pbCommon.BaseResp{StatusMessage: ``}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newMonitoringTestHandler(&fakeServiceStatusClient{getResp: tc.resp})
			rec, body := runMonitoringHandler(t, h.GetServiceStatus, "/api/v1/monitoring/services/logos.chat?service_name=logos.chat")

			// 详情没有本地探测兜底，解析失败必须是 5xx，不能 200 + data:null。
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500, body = %s", rec.Code, rec.Body.String())
			}
			if body["message"] != "invalid monitoring response" {
				t.Fatalf("message = %v, want invalid monitoring response", body["message"])
			}
			if _, hasData := body["data"]; hasData {
				t.Fatalf("失败响应不应携带 data: %v", body)
			}
		})
	}
}

func TestGetServiceStatus_UnwrapsData(t *testing.T) {
	h := newMonitoringTestHandler(&fakeServiceStatusClient{
		getResp: &pbCommon.BaseResp{
			StatusMessage: `{"data":{"service_name":"logos.chat","status":"UP","last_check_time":1700000000000}}`,
		},
	})
	rec, body := runMonitoringHandler(t, h.GetServiceStatus, "/api/v1/monitoring/services/logos.chat?service_name=logos.chat")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	data, ok := body["data"].(map[string]any)
	if !ok {
		t.Fatalf("data 类型 = %T, want map", body["data"])
	}
	if data["status"] != "UP" || data["service_name"] != "logos.chat" {
		t.Fatalf("data = %v", data)
	}
}

func TestGetServiceStatus_RPCFailureIs500(t *testing.T) {
	h := newMonitoringTestHandler(&fakeServiceStatusClient{getErr: errors.New("deadline exceeded")})
	rec, _ := runMonitoringHandler(t, h.GetServiceStatus, "/api/v1/monitoring/services/logos.chat?service_name=logos.chat")

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500, body = %s", rec.Code, rec.Body.String())
	}
}
