package pgsql

import "time"

// 连接池默认值。
//
// database/sql 的零值语义很容易踩坑：
//   - MaxOpenConns=0 表示「不限制」，不是「不连接」；
//   - MaxIdleConns=0 表示「不保留空闲连接」，每个请求都会新建连接；
//   - ConnMaxLifetime=0 表示连接永不过期。
//
// 配置漏项（尤其是本地开发配置）时，池会退化成上面任意一种极端形态，
// 表现为「偶发连接风暴」或「连接被中间件单方面断开」。所以在初始化处
// 做一次归一化，而不是假设每个环境的配置都完整。
const (
	defaultMaxOpenConns    = 50
	defaultMaxIdleConns    = 20
	defaultConnMaxLifetime = time.Hour
	defaultConnMaxIdleTime = 15 * time.Minute
)

// PoolSettings 是归一化后的连接池参数。
type PoolSettings struct {
	MaxOpen     int
	MaxIdle     int
	MaxLifetime time.Duration
	MaxIdleTime time.Duration
}

// NormalizePoolSettings 把配置收敛到安全范围：
//   - open<=0 → 50；
//   - idle<=0 → min(20, open)，idle>open → open（空闲连接不可能多于总连接）；
//   - lifetime<=0 → 1h；
//   - idleTime 默认 15m，且不超过 lifetime。
func NormalizePoolSettings(open, idle, lifetimeSeconds int) PoolSettings {
	if open <= 0 {
		open = defaultMaxOpenConns
	}
	if idle <= 0 {
		idle = defaultMaxIdleConns
	}
	if idle > open {
		idle = open
	}

	lifetime := time.Duration(lifetimeSeconds) * time.Second
	if lifetime <= 0 {
		lifetime = defaultConnMaxLifetime
	}

	idleTime := defaultConnMaxIdleTime
	if idleTime > lifetime {
		idleTime = lifetime
	}

	return PoolSettings{
		MaxOpen:     open,
		MaxIdle:     idle,
		MaxLifetime: lifetime,
		MaxIdleTime: idleTime,
	}
}
