package gateway

import (
	"time"

	"Logos/pkg/jwt"
)

// AuthRecheckInterval 长连接令牌复核周期。
//
// 认证只在建连时做一次，之后连接可能存活数小时：用户登出、改密（撤销全部令牌）、
// 令牌自然过期都不会让已建立的 WS/TCP 连接自动断开，客户端仍能通过这条「已失效
// 的凭证」继续收发消息。HTTP 接口每次请求都校验，长连接却没有，这是最常见的一类
// 权限残留。
//
// 复核周期是「撤销生效延迟」与「Redis 查询量」的权衡：
//   - 每次复核都是一次 Redis Exists/Get；5 分钟意味着单连接 12 次/小时，
//     1 万连接约 2k QPS 的读，量级可控；
//   - 同时它决定了最坏情况下登出后仍可发消息的窗口，5 分钟对 IM 业务可接受。
//     若安全要求更严，可下调到 1 分钟，代价是 Redis 查询量线性上升。
const AuthRecheckInterval = 5 * time.Minute

// AuthFailureReason 长连接复核失败原因，用于分流日志与指标标签。
type AuthFailureReason string

const (
	// AuthFailureNone 复核通过，连接可以继续使用。
	AuthFailureNone AuthFailureReason = ""
	// AuthFailureInvalid 令牌声明缺失（没有 user_id 等），理论上不应出现在已认证连接上。
	AuthFailureInvalid AuthFailureReason = "invalid"
	// AuthFailureRevoked 令牌已被撤销（单令牌拉黑或用户级撤销水位线）。
	AuthFailureRevoked AuthFailureReason = "revoked"
	// AuthFailureExpired 令牌自然过期。
	AuthFailureExpired AuthFailureReason = "expired"
)

// AuthRecheckDue 判断是否到了下一次复核时间。
//
// last 为零值（尚未复核过）时视为到期：调用方在建连时通常只在下一轮 ticker
// 才第一次复核，零值语义必须是「立刻复核」而不是「永远不复核」。
func AuthRecheckDue(last, now time.Time, interval time.Duration) bool {
	if interval <= 0 {
		return false
	}
	if last.IsZero() {
		return true
	}
	return !now.Before(last.Add(interval))
}

// ClassifyAuthFailure 把「撤销查询结果 + 令牌声明 + 当前时间」收敛成一个失败原因。
//
// 做成纯函数是为了让优先级可断言、可单测：
//  1. 声明本身无效 → invalid（无法判断撤销状态，先拒绝）；
//  2. 已被撤销 → revoked（即使令牌也已过期，撤销是更明确的业务动作，日志更有价值）；
//  3. 过期 → expired。
//
// 注意用 !now.Before(exp) 而不是 now.After(exp)：恰好在 exp 时刻令牌已失效，
// 边界上必须按过期处理，否则会出现「令牌到期那一秒仍然放行」的窗口。
func ClassifyAuthFailure(claims *jwt.Claims, revoked bool, now time.Time) AuthFailureReason {
	if claims == nil || claims.UserID == "" {
		return AuthFailureInvalid
	}
	if revoked {
		return AuthFailureRevoked
	}
	if claims.ExpiresAt == nil || !now.Before(claims.ExpiresAt.Time) {
		return AuthFailureExpired
	}
	return AuthFailureNone
}
