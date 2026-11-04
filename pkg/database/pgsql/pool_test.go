package pgsql

import (
	"testing"
	"time"
)

func TestNormalizePoolSettings(t *testing.T) {
	cases := []struct {
		name                    string
		open, idle, lifetimeSec int
		want                    PoolSettings
	}{
		{
			name: "全零值回落默认",
			want: PoolSettings{
				MaxOpen: 50, MaxIdle: 20,
				MaxLifetime: time.Hour, MaxIdleTime: 15 * time.Minute,
			},
		},
		{
			name: "负数按未配置处理",
			open: -1, idle: -5, lifetimeSec: -10,
			want: PoolSettings{
				MaxOpen: 50, MaxIdle: 20,
				MaxLifetime: time.Hour, MaxIdleTime: 15 * time.Minute,
			},
		},
		{
			name: "空闲连接不能多于总连接",
			open: 10, idle: 100, lifetimeSec: 3600,
			want: PoolSettings{
				MaxOpen: 10, MaxIdle: 10,
				MaxLifetime: time.Hour, MaxIdleTime: 15 * time.Minute,
			},
		},
		{
			name: "合法值原样保留",
			open: 50, idle: 20, lifetimeSec: 1800,
			want: PoolSettings{
				MaxOpen: 50, MaxIdle: 20,
				MaxLifetime: 30 * time.Minute, MaxIdleTime: 15 * time.Minute,
			},
		},
		{
			name: "lifetime 比 idleTime 还短时取更短值",
			open: 5, idle: 2, lifetimeSec: 60,
			want: PoolSettings{
				MaxOpen: 5, MaxIdle: 2,
				MaxLifetime: time.Minute, MaxIdleTime: time.Minute,
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := NormalizePoolSettings(c.open, c.idle, c.lifetimeSec)
			if got != c.want {
				t.Fatalf("NormalizePoolSettings(%d,%d,%d) = %+v, want %+v",
					c.open, c.idle, c.lifetimeSec, got, c.want)
			}
		})
	}
}

func TestShouldWaitBeforeNextAttempt(t *testing.T) {
	cases := []struct {
		name              string
		attempt, attempts int
		want              bool
	}{
		{"只尝试一次不等待", 0, 1, false},
		{"还有下一次要等待", 0, 3, true},
		{"中间一次要等待", 1, 3, true},
		{"最后一次不等待", 2, 3, false},
		{"attempts 非法时不等待", 0, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := shouldWaitBeforeNextAttempt(c.attempt, c.attempts); got != c.want {
				t.Fatalf("shouldWaitBeforeNextAttempt(%d,%d) = %v, want %v",
					c.attempt, c.attempts, got, c.want)
			}
		})
	}
}
