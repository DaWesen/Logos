package scache

import (
	"context"
	"time"
)

// Decision 是一次判定的可解释结论。
//
// 用枚举而不是字符串拼接：日志、指标和测试共用同一组常量，
// 改文案时不会悄悄漏掉某个分支（禁区 X10）。
type Decision string

const (
	HitExact     Decision = "hit_exact"     // 归一化后完全相同
	HitVector    Decision = "hit_vector"    // 相似度达到 high
	HitJudge     Decision = "hit_judge"     // 灰区经判官裁决为 SAME
	MissSlot     Decision = "miss_slot"     // 槽位冲突，保守否决
	MissLowSim   Decision = "miss_low_sim"  // 相似度低于 mid
	MissJudge    Decision = "miss_judge"    // 判官判 DIFFERENT / 超时 / 不可解析
	MissNoSlots  Decision = "miss_no_slots" // 指代短句，脱离上下文无意义
	MissDisabled Decision = "miss_disabled" // 开关关闭或前置条件不满足
	MissError    Decision = "miss_error"    // 依赖故障，降级为 miss
	MissShadowed Decision = "miss_shadowed"
)

// Hit 表示该判定是否真的把答案返回给了调用方。
// shadow 模式下的命中统一记为 MissShadowed，因此这里为 false。
func (d Decision) Hit() bool {
	switch d {
	case HitExact, HitVector, HitJudge:
		return true
	default:
		return false
	}
}

// Mode 是灰度三态。
type Mode string

const (
	ModeOff    Mode = "off"
	ModeShadow Mode = "shadow"
	ModeOn     Mode = "on"
)

// ParseMode 解析配置值，空值按 off 处理。
func ParseMode(raw string) (Mode, error) {
	switch Mode(raw) {
	case "", ModeOff:
		return ModeOff, nil
	case ModeShadow:
		return ModeShadow, nil
	case ModeOn:
		return ModeOn, nil
	default:
		return ModeOff, &ConfigError{Field: "mode", Reason: "must be off|shadow|on"}
	}
}

// EmbedFunc 把文本映射成向量。实现方负责超时与鉴权；
// 缓存层只保证「失败即 miss」。
type EmbedFunc func(ctx context.Context, text string) ([]float32, error)

// LookupRequest 是一次读请求。
//
// Vector 允许调用方复用同一轮已经算好的向量，避免 Lookup/Store 各调一次
// embedding。Embed 为空时直接按 miss 处理，绝不在缓存层猜一个默认模型。
type LookupRequest struct {
	Scope  Scope
	Query  string
	Vector []float32
	Embed  EmbedFunc
	// Mode 为空时跟随全局配置；per-Bot 灰度通过这里覆盖。
	Mode Mode
}

// LookupResult 是读请求的完整结论。
//
// 即使未命中也会带回 Decision/Similarity/ScopeHash：线上排障时
// 「为什么没命中」和「为什么命中」同样重要（G6）。
type LookupResult struct {
	Decision   Decision
	Answer     string
	EntryID    string
	Similarity float64
	ScopeHash  string
	EntryAge   time.Duration
	Vector     []float32
	Candidates int
	// ShadowDecision 记录 shadow 模式下「本会返回」的判定。
	// Decision 为 MissShadowed 时它才有意义。
	ShadowDecision Decision
}

// StoreRequest 是一次写请求。
//
// Source/ToolCalled/InjectionGuardHit 是准入判断的输入：多步规划、
// 工具副作用轮、注入护栏命中的答案都不允许复用（§6.4）。
type StoreRequest struct {
	Scope             Scope
	Query             string
	Answer            string
	Vector            []float32
	Embed             EmbedFunc
	Source            string
	ToolCalled        bool
	InjectionGuardHit bool
	Temperature       float64
	Mode              Mode
}
