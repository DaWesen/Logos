package grpcserver

import "Logos/config"

// gRPC 单条消息上限。
//
// 配置项 grpc.server.max_recv_msg_size / max_send_msg_size 很早就写进了
// configmap，但代码从来没有读取过——实际生效的一直是 gRPC 默认的 4MB。
// 这类「配置写了但没生效」最危险：改配置没有任何效果，直到某天要传大 payload
// 才在客户端看到 ResourceExhausted。
const (
	defaultMaxMsgSize = 4 << 20  // 4MB，与 gRPC 默认值一致
	maxAllowedMsgSize = 64 << 20 // 64MB，防止手滑写成 1GB
)

// ResolveMsgSize 把配置值收敛到 [4MB, 64MB]：
//   - <=0 视为未配置，用 4MB；
//   - 超过 64MB 截断，避免一次配置错误放大内存风险。
func ResolveMsgSize(v int) int {
	switch {
	case v <= 0:
		return defaultMaxMsgSize
	case v > maxAllowedMsgSize:
		return maxAllowedMsgSize
	default:
		return v
	}
}

// MsgSizeLimits 返回服务端/客户端应使用的收发上限。
// 配置尚未加载时退回默认值，保证单测和独立使用不依赖配置文件。
func MsgSizeLimits() (recv, send int) {
	cfg := config.TryGetConfig()
	if cfg == nil {
		return defaultMaxMsgSize, defaultMaxMsgSize
	}
	return ResolveMsgSize(cfg.GRPC.Server.MaxRecvMsgSize),
		ResolveMsgSize(cfg.GRPC.Server.MaxSendMsgSize)
}

// ClientMsgSizeLimits 返回客户端调用应使用的收发上限。
// 客户端与服务端分开配置：接收上限对应「服务端发多大的响应」，
// 与 server 的 recv 并不是同一个方向，不能直接复用。
func ClientMsgSizeLimits() (recv, send int) {
	cfg := config.TryGetConfig()
	if cfg == nil {
		return defaultMaxMsgSize, defaultMaxMsgSize
	}
	return ResolveMsgSize(cfg.GRPC.Client.MaxRecvMsgSize),
		ResolveMsgSize(cfg.GRPC.Client.MaxSendMsgSize)
}
