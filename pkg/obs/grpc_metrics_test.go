package obs

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func collectObsMetrics(t *testing.T) metricdata.ResourceMetrics {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := obsTestReader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("采集指标失败: %v", err)
	}
	return rm
}

// grpcRequestCount 返回指定 method/code 组合的累计请求数。
// 第二个返回值表示该组合是否存在数据点：「没有请求」与「计数为 0」必须能区分。
func grpcRequestCount(t *testing.T, method, code string) (int64, bool) {
	t.Helper()
	for _, sm := range collectObsMetrics(t).ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "logos.grpc.request.count" {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				t.Fatalf("请求计数指标类型 = %T", m.Data)
			}
			for _, p := range sum.DataPoints {
				mv, mok := p.Attributes.Value(attribute.Key("method"))
				cv, cok := p.Attributes.Value(attribute.Key("code"))
				if !mok || !cok {
					continue
				}
				if mv.AsString() == method && cv.AsString() == code {
					return p.Value, true
				}
			}
		}
	}
	return 0, false
}

func grpcRequestDurationSum(t *testing.T, method, code string) (float64, bool) {
	t.Helper()
	for _, sm := range collectObsMetrics(t).ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "logos.grpc.request.duration" {
				continue
			}
			hist, ok := m.Data.(metricdata.Histogram[float64])
			if !ok {
				t.Fatalf("请求耗时指标类型 = %T", m.Data)
			}
			for _, p := range hist.DataPoints {
				mv, mok := p.Attributes.Value(attribute.Key("method"))
				cv, cok := p.Attributes.Value(attribute.Key("code"))
				if !mok || !cok {
					continue
				}
				if mv.AsString() == method && cv.AsString() == code {
					return p.Sum, true
				}
			}
		}
	}
	return 0, false
}

func TestUnaryServerMetricsInterceptor_RecordsSuccessAndFailure(t *testing.T) {
	meters := InitServiceMeters("bot")
	interceptor := meters.UnaryServerMetricsInterceptor()
	const method = "/bot.BotService/Chat"

	if _, err := interceptor(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: method},
		func(context.Context, any) (any, error) {
			time.Sleep(2 * time.Millisecond)
			return "ok", nil
		}); err != nil {
		t.Fatalf("成功请求返回错误: %v", err)
	}

	wantErr := status.Error(codes.InvalidArgument, "bad request")
	if _, err := interceptor(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: method},
		func(context.Context, any) (any, error) {
			return nil, wantErr
		}); err == nil {
		t.Fatal("失败请求没有把 handler 错误透传出去")
	}

	if got, ok := grpcRequestCount(t, method, codes.OK.String()); !ok || got != 1 {
		t.Fatalf("成功请求计数 = %d (存在=%v)，期望 1", got, ok)
	}
	if got, ok := grpcRequestCount(t, method, codes.InvalidArgument.String()); !ok || got != 1 {
		t.Fatalf("失败请求计数 = %d (存在=%v)，期望 1", got, ok)
	}

	// 成功请求 sleep 了 2ms，耗时必须落在对应 code 的直方图里。
	if sum, ok := grpcRequestDurationSum(t, method, codes.OK.String()); !ok || sum < 0.002 {
		t.Fatalf("成功请求耗时 sum = %v (存在=%v)，期望 >= 0.002s", sum, ok)
	}
}

// TestUnaryServerMetricsInterceptor_DoesNotFabricateTraffic 是「假指标」的回归测试：
// 只创建 meters、不产生任何调用时，监控上不能凭空出现请求。
func TestUnaryServerMetricsInterceptor_DoesNotFabricateTraffic(t *testing.T) {
	_ = InitServiceMeters("bot")
	const method = "/bot.BotService/NeverCalled"

	if got, ok := grpcRequestCount(t, method, codes.OK.String()); ok {
		t.Fatalf("未收到请求却出现计数 %d：指标在造假", got)
	}
	if _, ok := grpcRequestDurationSum(t, method, codes.OK.String()); ok {
		t.Fatal("未收到请求却出现耗时数据点：指标在造假")
	}
}

func TestUnaryServerMetricsInterceptor_NilMetersIsPassthrough(t *testing.T) {
	var meters *ServiceMeters
	interceptor := meters.UnaryServerMetricsInterceptor()

	called := false
	got, err := interceptor(context.Background(), "req", &grpc.UnaryServerInfo{FullMethod: "/x.Y/Z"},
		func(context.Context, any) (any, error) {
			called = true
			return "resp", nil
		})
	if err != nil || !called || got != "resp" {
		t.Fatalf("nil meters 拦截器应原样透传: called=%v got=%v err=%v", called, got, err)
	}
}
