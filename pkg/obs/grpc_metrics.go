package obs

import (
	"context"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

// UnaryServerMetricsInterceptor 用真实的 gRPC 调用驱动 ServiceMeters。
//
// 指标的价值来自「一条数据点对应一次真实请求」。此前 Bot 服务用一个每 5s
// 醒一次的 goroutine 造指标：RequestCount 无条件 +1、延迟对
// time.Now().UnixNano() 取模、活跃连接数在 1..10 之间随机。这种数据的危害
// 比没有指标更大：
//   - 「没有请求」与「被打爆」在监控上完全一样，容量规划失去依据；
//   - 随机延迟会污染 P99，据此配置的告警阈值从头就是错的；
//   - 真正的断流故障被掩盖——dashboard 永远有数，没人会去查业务日志。
//
// 这里把打点绑到真实调用上：只有进入 handler 的请求才计数，code 标签让失败
// 请求单独可见，而不是被请求总量稀释。
func (m *ServiceMeters) UnaryServerMetricsInterceptor() grpc.UnaryServerInterceptor {
	if m == nil {
		return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
			return handler(ctx, req)
		}
	}

	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		resp, err := handler(ctx, req)
		m.RecordRequest(ctx, info.FullMethod, status.Code(err).String(), time.Since(start).Seconds())
		return resp, err
	}
}
