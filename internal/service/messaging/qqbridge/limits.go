package qqbridge

import (
	"time"

	"Logos/config"
)

// 并发与超时的默认值与硬上限。
//
// 配置可以调，但不能调成 0 或 100000：0 会让 chan 容量为 0，
// 所有请求立即降级；过大则失去限流意义。所有边界在构造时归一化，
// 而不是散落在各个调用点。
const (
	defaultFastConcurrency = 4
	maxFastConcurrency     = 64

	defaultMediaConcurrency = 8
	maxMediaConcurrency     = 64

	defaultMediaMaxMB = 100
	maxMediaMaxMB     = 1024

	defaultMediaTimeout = 90 * time.Second
	minMediaTimeout     = 5 * time.Second
	maxMediaTimeout     = 10 * time.Minute
)

// fastConcurrency 返回归一化后的快速路径并发上限。
func fastConcurrency(cfg *config.Config) int {
	if cfg == nil {
		return defaultFastConcurrency
	}
	return clampInt(cfg.QQBridge.FastConcurrency, 1, maxFastConcurrency, defaultFastConcurrency)
}

// mediaConcurrency 返回归一化后的媒体中转并发上限。
func mediaConcurrency(cfg *config.Config) int {
	if cfg == nil {
		return defaultMediaConcurrency
	}
	return clampInt(cfg.QQBridge.MediaConcurrency, 1, maxMediaConcurrency, defaultMediaConcurrency)
}

// mediaMaxBytes 返回归一化后的单文件上限（字节）。
func mediaMaxBytes(cfg *config.Config) int64 {
	mb := defaultMediaMaxMB
	if cfg != nil {
		mb = clampInt(cfg.QQBridge.MediaMaxBytesMB, 1, maxMediaMaxMB, defaultMediaMaxMB)
	}
	return int64(mb) << 20
}

// mediaTimeout 返回归一化后的媒体中转总预算。
func mediaTimeout(cfg *config.Config) time.Duration {
	seconds := 0
	if cfg != nil {
		seconds = cfg.QQBridge.MediaTimeoutSeconds
	}
	if seconds <= 0 {
		return defaultMediaTimeout
	}
	d := time.Duration(seconds) * time.Second
	if d < minMediaTimeout {
		return minMediaTimeout
	}
	if d > maxMediaTimeout {
		return maxMediaTimeout
	}
	return d
}

// clampInt 把 v 收敛到 [min,max]；v <= 0 视为「未配置」，返回默认值。
func clampInt(v, min, max, fallback int) int {
	if v <= 0 {
		return fallback
	}
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
