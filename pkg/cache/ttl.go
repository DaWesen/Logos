package cache

import "time"

// JitterTTL 给 TTL 加上 ±ratio 的随机抖动，返回
// ttl*(1-ratio+2*ratio*rnd)，rnd 取值 [0,1)。
//
// 为什么需要：同一批写入的缓存 key（例如服务启动后预热的一批实体）如果
// TTL 完全相同，就会在同一秒集中失效，请求同时回源形成「缓存雪崩」。
// 抖动把失效时间摊开，代价是单个 key 的 TTL 最多偏差 ratio。
//
// rnd 由调用方传入而不是内部调用随机数：纯函数可断言，测试不需要 sleep。
func JitterTTL(ttl time.Duration, ratio, rnd float64) time.Duration {
	if ttl <= 0 {
		return ttl
	}
	switch {
	case ratio <= 0:
		return ttl
	case ratio > 1:
		ratio = 1
	}
	if rnd < 0 {
		rnd = 0
	} else if rnd >= 1 {
		rnd = 0.999999
	}

	factor := 1 - ratio + 2*ratio*rnd
	return time.Duration(float64(ttl) * factor)
}
