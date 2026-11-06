package scache

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"Logos/pkg/cache"
)

func randFloat() float64 { return rand.Float64() }

// ConfigError 表示配置非法。启动期配置错误必须让进程起不来，
// 而不是带着一套「mid > high」的阈值运行到线上。
type ConfigError struct {
	Field  string
	Reason string
}

func (e *ConfigError) Error() string {
	return fmt.Sprintf("scache: invalid config %q: %s", e.Field, e.Reason)
}

// JudgeFunc 是灰区判官依赖的模型调用。prompt 已由缓存层拼好，
// 实现方只需返回模型原始文本。
type JudgeFunc func(ctx context.Context, prompt string) (string, error)

// Config 是语义缓存的全部可调参数。
//
// 所有影响行为的值都必须来自配置（C5/禁区 X9）：阈值、TTL、容量、
// 队列长度、回放节奏都不允许散落在代码里。
type Config struct {
	// CacheProvider 返回 pkg/cache 单例。返回 nil 表示 Redis 不可用，
	// 此时缓存退化为「纯进程内」：仍能命中，但重启后丢失。
	CacheProvider func() cache.Cache
	Judge         JudgeFunc

	Mode               Mode
	MidThreshold       float64
	HighThreshold      float64
	MaxEntriesPerScope int
	MaxTotalEntries    int
	MaxTotalBytes      int64
	EntryTTL           time.Duration
	ExactTTL           time.Duration
	PriceTTL           time.Duration
	JudgeEnabled       bool
	JudgeTimeout       time.Duration
	EmbedTimeout       time.Duration
	WriteQueueSize     int
	WriteWorkers       int
	CalibratedFor      string
	TopK               int
	EvictInterval      time.Duration
	ReplayChunkRunes   int
	ReplayBudget       time.Duration
	ReplayMaxDelay     time.Duration
	MinAnswerRunes     int
	MaxAnswerRunes     int
	SignalsTTL         time.Duration
	LoadRetry          time.Duration

	// Now/Rand 是测试注入点：TTL 抖动与过期判断不应依赖真实时钟。
	Now  func() time.Time
	Rand func() float64
}

// DefaultConfig 返回一套可直接运行的保守默认值。
//
// 阈值 0.90/0.95 只是「未标定前的占位」：docs 中明确要求真实阈值
// 来自 calib.jsonl 的脚本输出，mode=on 时 CalibratedFor 必填，
// 用配置强制提醒上线者先做标定。
func DefaultConfig() Config {
	return Config{
		Mode:               ModeOff,
		MidThreshold:       0.90,
		HighThreshold:      0.95,
		MaxEntriesPerScope: 200,
		MaxTotalEntries:    50000,
		MaxTotalBytes:      256 << 20,
		EntryTTL:           24 * time.Hour,
		ExactTTL:           24 * time.Hour,
		PriceTTL:           30 * time.Minute,
		JudgeEnabled:       true,
		JudgeTimeout:       300 * time.Millisecond,
		EmbedTimeout:       1500 * time.Millisecond,
		WriteQueueSize:     1024,
		WriteWorkers:       2,
		TopK:               5,
		EvictInterval:      time.Minute,
		ReplayChunkRunes:   24,
		ReplayBudget:       400 * time.Millisecond,
		ReplayMaxDelay:     15 * time.Millisecond,
		MinAnswerRunes:     50,
		MaxAnswerRunes:     4000,
		SignalsTTL:         2 * time.Minute,
		LoadRetry:          30 * time.Second,
		Now:                time.Now,
		Rand:               randFloat,
	}
}

// Validate 在 New 中调用，失败直接返回 error。
func (c Config) Validate() error {
	switch c.Mode {
	case ModeOff, ModeShadow, ModeOn:
	default:
		return &ConfigError{Field: "mode", Reason: "must be off|shadow|on"}
	}
	if c.MidThreshold <= 0 || c.HighThreshold <= 0 || c.MidThreshold >= c.HighThreshold || c.HighThreshold >= 1 {
		return &ConfigError{
			Field:  "thresholds",
			Reason: fmt.Sprintf("require 0 < mid < high < 1, got mid=%v high=%v", c.MidThreshold, c.HighThreshold),
		}
	}
	if c.Mode == ModeOn && c.CalibratedFor == "" {
		return &ConfigError{Field: "calibrated_for", Reason: "required when mode=on"}
	}
	if c.MaxEntriesPerScope <= 0 || c.MaxTotalEntries <= 0 || c.MaxTotalBytes <= 0 {
		return &ConfigError{Field: "capacity", Reason: "entry limits must be positive"}
	}
	if c.EntryTTL <= 0 || c.ExactTTL <= 0 || c.PriceTTL <= 0 {
		return &ConfigError{Field: "ttl", Reason: "must be positive"}
	}
	if c.WriteQueueSize <= 0 || c.WriteWorkers <= 0 {
		return &ConfigError{Field: "write", Reason: "queue size and workers must be positive"}
	}
	if c.TopK <= 0 {
		return &ConfigError{Field: "top_k", Reason: "must be positive"}
	}
	if c.MinAnswerRunes < 0 || c.MaxAnswerRunes <= c.MinAnswerRunes {
		return &ConfigError{Field: "answer_length", Reason: "require 0 <= min < max"}
	}
	if c.ReplayChunkRunes <= 0 || c.ReplayBudget < 0 || c.ReplayMaxDelay < 0 {
		return &ConfigError{Field: "replay", Reason: "chunk must be positive, budget/delay non-negative"}
	}
	return nil
}
