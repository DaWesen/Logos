package gateway

import "time"

// PingJitterFraction 心跳周期的默认抖动幅度（±10%）。
//
// 为什么需要抖动：网关滚动发布/重启后，成千上万条连接会在同一秒建立，
// 若 ping 周期是完全固定的 60s，它们会永远保持同一相位——每 60 秒出现一次
// 「全员同时 ping」的瞬时尖峰（写队列、CPU、网络包一起抖动），并在读超时
// 附近形成同步的集体重连。给每条连接加一点随机偏移即可把尖峰摊平。
//
// 幅度取 10% 是权衡：太小摊不开（尖峰仍显著），太大意味着个别连接的心跳
// 周期缩短到 54s，对弱网客户端更敏感。10% 在 WSPingInterval=60s 下是 ±6s，
// 与 WSReadTimeout 的 30s 裕量相比完全安全。
const PingJitterFraction = 0.10

// JitterDuration 返回带抖动的周期：base * (1 + fraction*(2*rnd-1))。
//
// rnd 由调用方注入（生产传 rand.Float64()，单测传固定值），保证行为可测且
// 不把随机源耦合进调度逻辑。参数做防御性收敛：
//   - base <= 0：直接返回，避免产生负数或零周期（零周期 ticker 会 panic）；
//   - fraction <= 0：退化为不抖动，便于用配置关掉该特性；
//   - fraction > 1：收敛到 1，保证结果非负（否则 rnd=0 时会得到负周期）；
//   - rnd 越界：夹到 [0,1]，调用方传错也不会得到越界周期。
func JitterDuration(base time.Duration, fraction, rnd float64) time.Duration {
	if base <= 0 {
		return base
	}
	if fraction <= 0 {
		return base
	}
	if fraction > 1 {
		fraction = 1
	}
	if rnd < 0 {
		rnd = 0
	}
	if rnd > 1 {
		rnd = 1
	}
	d := time.Duration(float64(base) * (1 + fraction*(2*rnd-1)))
	if d <= 0 {
		// fraction 收敛到 1 且 rnd=0 时理论上会得到 0 周期；time.NewTicker(0) 直接
		// panic，因此这里退化为不抖动而不是把非法周期交给运行时。
		return base
	}
	return d
}
