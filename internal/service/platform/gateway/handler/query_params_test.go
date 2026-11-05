package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	pbCommon "Logos/proto_gen/common"
	pb "Logos/proto_gen/monitoring"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"
)

// fakeMonitoringClient 记录网关下发的查询条件；calls 用于断言
// 非法参数没有穿透到下游（否则下游会真的按「不限时间」跑一次全表扫描）。
type fakeMonitoringClient struct {
	pb.MonitoringServiceClient
	calls    int
	metric   *pb.QueryMetricReq
	log      *pb.QueryLogReq
	alert    *pb.QueryAlertReq
}

func (f *fakeMonitoringClient) QueryMetrics(_ context.Context, in *pb.QueryMetricReq, _ ...grpc.CallOption) (*pb.MetricResp, error) {
	f.calls++
	f.metric = in
	return &pb.MetricResp{BaseResp: okBaseResp()}, nil
}

func (f *fakeMonitoringClient) QueryLogs(_ context.Context, in *pb.QueryLogReq, _ ...grpc.CallOption) (*pb.LogResp, error) {
	f.calls++
	f.log = in
	return &pb.LogResp{BaseResp: okBaseResp()}, nil
}

func (f *fakeMonitoringClient) QueryAlerts(_ context.Context, in *pb.QueryAlertReq, _ ...grpc.CallOption) (*pb.AlertResp, error) {
	f.calls++
	f.alert = in
	return &pb.AlertResp{BaseResp: okBaseResp()}, nil
}

func okBaseResp() *pbCommon.BaseResp { return &pbCommon.BaseResp{} }

func newMonitoringRouter(client pb.MonitoringServiceClient) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := &Handler{MonitoringClient: client}
	r := gin.New()
	r.GET("/monitoring/metric", h.QueryMetrics)
	r.GET("/monitoring/log", h.QueryLogs)
	r.GET("/monitoring/alert", h.QueryAlerts)
	return r
}

func doQuery(t *testing.T, r *gin.Engine, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// 时间参数是过滤条件，不是「可选的装饰」：`?start_time=abc` 静默变 0 后
// DAO 会跳过时间条件，接口照样 200，但返回的是全量数据——比报错更难排查。
func TestQueryEndpoints_RejectNonNumericTime(t *testing.T) {
	paths := []string{
		"/monitoring/metric?start_time=abc",
		"/monitoring/metric?end_time=abc",
		"/monitoring/metric?start_time=-1",
		"/monitoring/log?start_time=abc",
		"/monitoring/log?end_time=abc",
		"/monitoring/alert?start_time=abc",
		"/monitoring/alert?end_time=abc",
		"/monitoring/metric?start_time=99999999999999999999999",
	}

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			client := &fakeMonitoringClient{}
			rec := doQuery(t, newMonitoringRouter(client), path)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("HTTP 状态 = %d, want 400, body = %s", rec.Code, rec.Body.String())
			}
			if client.calls != 0 {
				t.Fatalf("非法时间参数不应调用下游，实际调用 %d 次", client.calls)
			}
		})
	}
}

// 区间写反时 SQL 恒为空集，调用方只会看到「没有数据」，看不出是自己写反了。
func TestQueryEndpoints_RejectInvertedTimeRange(t *testing.T) {
	for _, path := range []string{
		"/monitoring/metric?start_time=200&end_time=100",
		"/monitoring/log?start_time=200&end_time=100",
		"/monitoring/alert?start_time=200&end_time=100",
	} {
		t.Run(path, func(t *testing.T) {
			client := &fakeMonitoringClient{}
			rec := doQuery(t, newMonitoringRouter(client), path)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("HTTP 状态 = %d, want 400, body = %s", rec.Code, rec.Body.String())
			}
			if client.calls != 0 {
				t.Fatalf("区间非法时不应调用下游，实际调用 %d 次", client.calls)
			}
		})
	}
}

func TestQueryMetrics_PassesValidFilters(t *testing.T) {
	client := &fakeMonitoringClient{}
	rec := doQuery(t, newMonitoringRouter(client),
		"/monitoring/metric?service_name=logos.gateway&type=7&start_time=100&end_time=200")

	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP 状态 = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	if client.metric.GetType() != pb.MetricType_METRIC_TYPE_LATENCY {
		t.Fatalf("type = %v, want LATENCY", client.metric.GetType())
	}
	if client.metric.GetStartTime() != 100 || client.metric.GetEndTime() != 200 {
		t.Fatalf("时间区间 = [%d, %d], want [100, 200]",
			client.metric.GetStartTime(), client.metric.GetEndTime())
	}
	if client.metric.GetServiceName() != "logos.gateway" {
		t.Fatalf("service_name = %q", client.metric.GetServiceName())
	}
}

// 默认值必须保持「CPU 指标」，避免把原来不传 type 的行为悄悄改掉。
func TestQueryMetrics_DefaultsToCPUWhenTypeAbsent(t *testing.T) {
	client := &fakeMonitoringClient{}
	doQuery(t, newMonitoringRouter(client), "/monitoring/metric")

	if client.metric.GetType() != pb.MetricType_METRIC_TYPE_CPU {
		t.Fatalf("缺省 type = %v, want CPU", client.metric.GetType())
	}
}

// 枚举越界值不是笔误就是客户端版本不匹配；继续下发会让下游按 0 号语义执行。
func TestQueryEndpoints_RejectOutOfRangeEnum(t *testing.T) {
	cases := []struct {
		name string
		path string
	}{
		{"指标类型非数字", "/monitoring/metric?type=abc"},
		{"指标类型越界", "/monitoring/metric?type=9"},
		{"指标类型为负", "/monitoring/metric?type=-1"},
		{"告警级别非数字", "/monitoring/alert?level=abc"},
		{"告警级别越界", "/monitoring/alert?level=5"},
		{"告警级别为负", "/monitoring/alert?level=-1"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := &fakeMonitoringClient{}
			rec := doQuery(t, newMonitoringRouter(client), tc.path)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("HTTP 状态 = %d, want 400, body = %s", rec.Code, rec.Body.String())
			}
			if client.calls != 0 {
				t.Fatalf("非法枚举不应调用下游，实际调用 %d 次", client.calls)
			}
		})
	}
}

// level=0 是 UNSPECIFIED，语义是「全部级别」，必须转成 nil（不过滤）。
// 旧实现只要参数非空就塞指针，DAO 再按 level=0 过滤，而库里只有 1..4，
// 于是「全部级别」筛选恒为空。
func TestQueryAlerts_ZeroLevelMeansNoFilter(t *testing.T) {
	client := &fakeMonitoringClient{}
	rec := doQuery(t, newMonitoringRouter(client), "/monitoring/alert?level=0")

	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP 状态 = %d, want 200", rec.Code)
	}
	if client.alert.Level != nil {
		t.Fatalf("level=0 应表示不过滤，实际下发 %v", *client.alert.Level)
	}
}

// `?resolved=yes` 旧实现两条分支都不命中，等于悄悄取消过滤。
func TestQueryAlerts_RejectsInvalidResolved(t *testing.T) {
	client := &fakeMonitoringClient{}
	rec := doQuery(t, newMonitoringRouter(client), "/monitoring/alert?resolved=yes")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("HTTP 状态 = %d, want 400", rec.Code)
	}
	if client.calls != 0 {
		t.Fatalf("非法布尔不应调用下游，实际调用 %d 次", client.calls)
	}
}

func TestQueryAlerts_ResolvedTriState(t *testing.T) {
	cases := []struct {
		name  string
		query string
		want  *bool
	}{
		{"未传", "", nil},
		{"true", "resolved=true", boolPtr(true)},
		{"false", "resolved=false", boolPtr(false)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := &fakeMonitoringClient{}
			doQuery(t, newMonitoringRouter(client), "/monitoring/alert?"+tc.query)

			got := client.alert.Resolved
			if tc.want == nil {
				if got != nil {
					t.Fatalf("未传 resolved 应不过滤，实际 %v", *got)
				}
				return
			}
			if got == nil || *got != *tc.want {
				t.Fatalf("resolved = %v, want %v", got, *tc.want)
			}
		})
	}
}

func boolPtr(v bool) *bool { return &v }
