package gateway

import "time"

// PresenceTouchInterval presence 续期的本地节流间隔。
//
// 续期路径（WS pong、WS/TCP 心跳）在客户端活跃时可能每秒触发多次，
// 每次都是一次 Redis HGetAll + HSet。RedisPresenceStore 的 pruneAndCount 还会
// 顺带读回整个用户会话哈希，等于把「心跳」放大成一次小规模读放大。
//
// 由于 PresenceStaleAfter 是 3 分钟，而续期只需要保证「在过期前至少写一次」，
// 15 秒的本地节流把写放大降低到原来的 1/15 左右，同时保留 180s/15s = 12 倍的安全裕量：
// 即使客户端心跳间隔接近读超时，也不会因为跳过若干次续期而被判离线。
const PresenceTouchInterval = 15 * time.Second

// ShouldTouchPresence 判断本次续期是否应该真正打到存储。
//
// last 为零值（会话刚建立、从未续期过）时立即续期：建连路径本身已经写过一次，
// 但把零值当「刚写过」会让新建会话在最初 15 秒内得不到保护，是更容易出错的方向。
func ShouldTouchPresence(last, now time.Time, interval time.Duration) bool {
	if interval <= 0 {
		return true
	}
	if last.IsZero() {
		return true
	}
	return !now.Before(last.Add(interval))
}
