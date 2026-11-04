package qqbridge

import (
	"testing"
	"time"

	"Logos/config"
)

func TestFastConcurrency_NormalizesBounds(t *testing.T) {
	cases := []struct {
		name string
		cfg  *config.Config
		want int
	}{
		{"nil 配置用默认值", nil, defaultFastConcurrency},
		{"未配置用默认值", &config.Config{}, defaultFastConcurrency},
		{"负数用默认值", &config.Config{QQBridge: config.QQBridge{FastConcurrency: -3}}, defaultFastConcurrency},
		{"合法值透传", &config.Config{QQBridge: config.QQBridge{FastConcurrency: 12}}, 12},
		{"超大值被截断", &config.Config{QQBridge: config.QQBridge{FastConcurrency: 1 << 20}}, maxFastConcurrency},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := fastConcurrency(tc.cfg); got != tc.want {
				t.Fatalf("got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestMediaConcurrency_NeverZero(t *testing.T) {
	// 容量为 0 的 chan 会让所有请求立即降级，必须被归一化掉。
	if got := mediaConcurrency(&config.Config{QQBridge: config.QQBridge{MediaConcurrency: 0}}); got != defaultMediaConcurrency {
		t.Fatalf("0 应回落到默认值，got %d", got)
	}
	if got := mediaConcurrency(&config.Config{QQBridge: config.QQBridge{MediaConcurrency: 999}}); got != maxMediaConcurrency {
		t.Fatalf("超大值应被截断，got %d", got)
	}
}

func TestMediaMaxBytes_ConvertsMBToBytes(t *testing.T) {
	if got := mediaMaxBytes(&config.Config{QQBridge: config.QQBridge{MediaMaxBytesMB: 50}}); got != 50<<20 {
		t.Fatalf("50MB 应换算为 %d 字节，got %d", int64(50)<<20, got)
	}
	if got := mediaMaxBytes(nil); got != int64(defaultMediaMaxMB)<<20 {
		t.Fatalf("nil 配置应回落到默认值，got %d", got)
	}
}

func TestMediaTimeout_ClampsAndDefaults(t *testing.T) {
	cases := []struct {
		name    string
		seconds int
		want    time.Duration
	}{
		{"未配置", 0, defaultMediaTimeout},
		{"负数", -10, defaultMediaTimeout},
		{"过小被抬到下限", 1, minMediaTimeout},
		{"合法值", 45, 45 * time.Second},
		{"过大被压到上限", 3600, maxMediaTimeout},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{QQBridge: config.QQBridge{MediaTimeoutSeconds: tc.seconds}}
			if got := mediaTimeout(cfg); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNewBridge_UsesConfiguredConcurrency(t *testing.T) {
	bridge := NewBridge(&config.Config{QQBridge: config.QQBridge{FastConcurrency: 16}})
	if cap(bridge.fastSlots) != 16 {
		t.Fatalf("快速路径容量应为 16，实际 %d", cap(bridge.fastSlots))
	}
}

func TestNewMediaRelay_UsesConfiguredLimits(t *testing.T) {
	relay := NewMediaRelay(&config.Config{QQBridge: config.QQBridge{
		MediaConcurrency: 3,
		MediaMaxBytesMB:  20,
	}})
	if cap(relay.sem) != 3 {
		t.Fatalf("媒体并发容量应为 3，实际 %d", cap(relay.sem))
	}
	if relay.maxBytes != 20<<20 {
		t.Fatalf("单文件上限应为 20MB，实际 %d", relay.maxBytes)
	}
}
