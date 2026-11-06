package planner

import (
	"context"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/rand/v2"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// 执行器级哨兵错误。
//
// 失败重试必须基于类型而不是错误文本：上游把错误包一层、或文案改一个字，
// 字符串匹配就会悄悄失效，表现为"偶发不重试"这种很难复现的问题。
var (
	// ErrBudgetExhausted 本轮预算（调用次数/墙钟）已用尽。
	ErrBudgetExhausted = errors.New("planner budget exhausted")
	// ErrDeadlock 依赖图无法继续推进，正常不应出现，出现即调度 bug。
	ErrDeadlock = errors.New("planner dependency graph deadlocked")
	// ErrToolNotFound 工具不存在，重试没有意义。
	ErrToolNotFound = errors.New("tool not found")
	// ErrInvalidArgs 工具参数非法，重试没有意义。
	ErrInvalidArgs = errors.New("invalid tool arguments")
	// ErrPermission 权限不足，重试没有意义。
	ErrPermission = errors.New("permission denied")
	// ErrRateLimited 上游限流，属于可重试的瞬时失败。
	ErrRateLimited = errors.New("rate limited")
	// ErrTransient 供工具层包装超时、5xx、连接重置等瞬时错误。
	ErrTransient = errors.New("transient failure")
	// ErrEmptyStepOutput 步骤成功返回但既无正文也无事实，视为失败。
	// 空字符串如果被当成成功，会进入综合阶段并产生一个"看起来正常"的空答案。
	ErrEmptyStepOutput = errors.New("empty step output")
)

// FailKind 失败分级，决定是否重试以及是否消耗重规划配额。
type FailKind int

const (
	// FailTransient 瞬时失败：超时、429、5xx、连接重置。
	FailTransient FailKind = iota
	// FailPermanent 永久失败：工具不存在、参数非法、权限不足、404。
	FailPermanent
	// FailContradiction 前提被证伪，来自 Violation 而不是 error。
	FailContradiction
)

// ClassifyError 把执行错误映射为失败等级。
//
// 默认按 permanent 处理是刻意保守：未知错误重试只会放大副作用与成本，
// 想获得重试能力必须在工具层显式包装 ErrTransient / ErrRateLimited。
func ClassifyError(err error) FailKind {
	if err == nil {
		return FailPermanent
	}
	switch {
	case errors.Is(err, context.Canceled):
		return FailPermanent
	case errors.Is(err, ErrRateLimited), errors.Is(err, ErrTransient),
		errors.Is(err, context.DeadlineExceeded):
		return FailTransient
	case errors.Is(err, ErrToolNotFound), errors.Is(err, ErrInvalidArgs),
		errors.Is(err, ErrPermission):
		return FailPermanent
	}
	var timeout interface{ Timeout() bool }
	if errors.As(err, &timeout) && timeout.Timeout() {
		return FailTransient
	}
	return FailPermanent
}

// RunStatus 运行级状态。
// 与 StepStatus 分开：日志里必须能区分"这一步失败了"和"整轮失败了"。
type RunStatus string

const (
	RunRunning         RunStatus = "running"
	RunCompleted       RunStatus = "completed"
	RunDegraded        RunStatus = "degraded"
	RunBudgetExhausted RunStatus = "budget_exhausted"
	RunBlocked         RunStatus = "blocked"
	RunFailed          RunStatus = "failed"
)

// Config 执行器配置。零值字段由 DefaultConfig 补齐。
type Config struct {
	// MaxConcurrency 层内并发上限；实际值还会被硬上限 4 截断。
	MaxConcurrency int
	// MaxLLMCalls 本轮允许的模型/工具调用次数上限。
	MaxLLMCalls int
	// MaxWallClock 本轮墙钟上限。
	MaxWallClock time.Duration
	// MaxReplans 整轮允许的重规划次数；0 表示使用默认值，
	// 需要显式关闭时用 DisableReplan 或 -1。
	MaxReplans int
	// DisableReplan 显式关闭重规划；关闭后失效步骤保留，直接产出部分结果。
	DisableReplan bool
	// MaxSteps patch 累积后的步骤总数上限。
	MaxSteps int
	// MaxRetries 瞬时失败的重试次数（不含首次执行）。
	MaxRetries int
	// RetryBase 首次重试的退避基数，之后指数增长。
	RetryBase time.Duration
}

// DefaultConfig 返回默认执行预算。
//
// 调用上限推导：1 次规划 + 8 步 × (1+1) 次 × 3 次 ReAct + 2 次重规划 × 4 次
// + 1 次综合 ≈ 70，留约 15% 余量后取 80。AvgReActCalls 需按线上指标校准。
func DefaultConfig() Config {
	return Config{
		MaxConcurrency: 4,
		MaxLLMCalls:    80,
		MaxWallClock:   180 * time.Second,
		MaxReplans:     MaxReplanCount,
		MaxSteps:       MaxTotalSteps,
		MaxRetries:     1,
		RetryBase:      500 * time.Millisecond,
	}
}

func (c Config) withDefaults() Config {
	def := DefaultConfig()
	if c.MaxConcurrency <= 0 {
		c.MaxConcurrency = def.MaxConcurrency
	}
	if c.MaxConcurrency > 4 {
		c.MaxConcurrency = 4
	}
	if c.MaxLLMCalls <= 0 {
		c.MaxLLMCalls = def.MaxLLMCalls
	}
	if c.MaxWallClock <= 0 {
		c.MaxWallClock = def.MaxWallClock
	}
	if c.MaxReplans == 0 {
		c.MaxReplans = def.MaxReplans
	}
	if c.MaxReplans < 0 || c.DisableReplan {
		c.MaxReplans = 0
	}
	if c.MaxSteps <= 0 {
		c.MaxSteps = def.MaxSteps
	}
	if c.MaxRetries < 0 {
		c.MaxRetries = 0
	}
	if c.RetryBase <= 0 {
		c.RetryBase = def.RetryBase
	}
	return c
}

// Budget 本轮执行预算，内部用原子量扣减，不阻塞等待。
type Budget struct {
	maxCalls int
	maxWall  time.Duration
	start    time.Time
	used     atomic.Int64
}

// NewBudget 按配置创建预算。
func NewBudget(cfg Config) *Budget {
	cfg = cfg.withDefaults()
	return &Budget{maxCalls: cfg.MaxLLMCalls, maxWall: cfg.MaxWallClock, start: time.Now()}
}

// TryReserve 预留 n 次调用；预算不足返回 false，由调用方降级而不是等待。
func (b *Budget) TryReserve(n int) bool {
	if b == nil || n <= 0 {
		return true
	}
	if b.RemainingWall() <= 0 {
		return false
	}
	for {
		cur := b.used.Load()
		if cur+int64(n) > int64(b.maxCalls) {
			return false
		}
		if b.used.CompareAndSwap(cur, cur+int64(n)) {
			return true
		}
	}
}

// Remaining 返回剩余调用次数。
func (b *Budget) Remaining() int {
	if b == nil {
		return 0
	}
	left := int64(b.maxCalls) - b.used.Load()
	if left < 0 {
		return 0
	}
	return int(left)
}

// RemainingWall 返回剩余墙钟时间。
func (b *Budget) RemainingWall() time.Duration {
	if b == nil {
		return 0
	}
	return b.maxWall - time.Since(b.start)
}

// Check 返回预算耗尽的具体原因。
func (b *Budget) Check() error {
	if b == nil {
		return nil
	}
	if b.Remaining() <= 0 {
		return fmt.Errorf("%w: 调用次数已用尽", ErrBudgetExhausted)
	}
	if b.RemainingWall() <= 0 {
		return fmt.Errorf("%w: 执行时间已用尽", ErrBudgetExhausted)
	}
	return nil
}

// StepExecutorV2 单步执行器。
//
// 返回结构化产物而不是裸字符串：facts / violations 是失效传播的输入，
// 让执行器直接返回结构体比事后解析自然语言可靠得多。
type StepExecutorV2 func(ctx context.Context, step PlanStep, priorResults string) (StepOutcome, error)

// AdaptStepExecutor 把旧的字符串执行器适配成 v2 执行器。
// 兼容路径不产生 facts / violations，解析交给调用方按需处理。
func AdaptStepExecutor(fn StepExecutor) StepExecutorV2 {
	return func(ctx context.Context, step PlanStep, priorResults string) (StepOutcome, error) {
		out, err := fn(ctx, step, priorResults)
		return StepOutcome{Output: out}, err
	}
}

// ProgressEvent 一条结构化进度事件。
type ProgressEvent struct {
	Type    string
	StepID  int
	Attempt int
	Message string
	At      time.Time
}

// 进度事件类型。
const (
	EventPlan       = "plan"
	EventStepStart  = "step_start"
	EventStepDone   = "step_done"
	EventStepFailed = "step_failed"
	EventStale      = "stale"
	EventReplan     = "replan"
	EventDone       = "done"
)

// ProgressListenerV2 结构化进度回调。
// 与旧的 func(string) 分开命名，避免破坏现有调用方的类型。
type ProgressListenerV2 func(ProgressEvent)

// TextProgressListener 把结构化事件适配成旧的人类可读文本。
func TextProgressListener(fn func(string)) ProgressListenerV2 {
	if fn == nil {
		return nil
	}
	return func(ev ProgressEvent) {
		switch ev.Type {
		case EventPlan, EventReplan, EventDone:
			fn(ev.Message)
		case EventStepStart:
			fn(fmt.Sprintf("▶ 执行步骤 %d：%s\n", ev.StepID, ev.Message))
		case EventStepDone:
			fn(fmt.Sprintf("✔ 步骤 %d 完成\n", ev.StepID))
		case EventStepFailed:
			fn(fmt.Sprintf("✖ 步骤 %d 失败：%s\n", ev.StepID, ev.Message))
		case EventStale:
			fn(fmt.Sprintf("⚠ 步骤 %d 结果失效：%s\n", ev.StepID, ev.Message))
		}
	}
}

// ExecuteOptions 执行期可选项。
type ExecuteOptions struct {
	Config   Config
	Listener ProgressListenerV2
	// Warnf 记录可恢复异常（例如模型编造了不存在的 violation step_id）。
	Warnf func(format string, args ...any)
}

// RunResult 整轮执行结果。
type RunResult struct {
	RunID     string
	Status    RunStatus
	Plan      *Plan
	Results   []StepResult
	Replans   int
	StartedAt time.Time
	Duration  time.Duration
	Err       error
}

// Execute 使用默认配置执行计划。
func (p *Planner) Execute(ctx context.Context, plan *Plan, exec StepExecutorV2) (*RunResult, error) {
	return p.ExecuteWith(ctx, plan, exec, ExecuteOptions{})
}

// ExecuteWith 按依赖分层执行计划，失败或前提被推翻时用 patch 局部重规划。
//
// 与线性版本的关键差异：
//   - 只重跑失效集合及其下游，未受影响的结果直接复用；
//   - 瞬时失败重试，不消耗重规划配额；
//   - 副作用步骤层内串行，且携带幂等键；
//   - 预算耗尽返回部分结果（degraded），而不是丢掉已经完成的工作。
func (p *Planner) ExecuteWith(ctx context.Context, plan *Plan, exec StepExecutorV2, opts ExecuteOptions) (*RunResult, error) {
	if plan == nil {
		return nil, fmt.Errorf("计划为空")
	}
	if exec == nil {
		return nil, fmt.Errorf("执行器为空")
	}
	cfg := opts.Config.withDefaults()
	for i := range plan.Steps {
		if plan.Steps[i].Status == "" {
			plan.Steps[i].Status = StatusPending
		}
	}
	if err := validatePlan(plan, cfg.MaxSteps); err != nil {
		return nil, err
	}
	if plan.nextID <= 0 {
		maxID := 0
		for _, s := range plan.Steps {
			if s.ID > maxID {
				maxID = s.ID
			}
		}
		plan.nextID = maxID + 1
	}

	run := &RunResult{
		RunID:     newRunID(),
		Status:    RunRunning,
		Plan:      plan,
		StartedAt: time.Now(),
	}
	budget := NewBudget(cfg)
	emitter := newEmitter(opts.Listener)
	if emitter != nil {
		defer emitter.Close()
	}
	emitter.emit(ProgressEvent{Type: EventPlan, Message: plan.FormatPlan()})

	if err := ctx.Err(); err != nil {
		run.Status = RunFailed
		run.Err = err
		return run.finish(), err
	}

	for {
		// 先判完成再看预算：预算刚好等于步骤数时，最后一层跑完就会耗尽，
		// 如果先 Check 会把一次成功执行误报成 budget_exhausted。
		if plan.AllSettled() {
			run.Status = RunCompleted
			break
		}
		if err := budget.Check(); err != nil {
			run.Status = RunBudgetExhausted
			run.Err = err
			break
		}

		layer := plan.ReadyLayer()
		if len(layer) == 0 {
			if plan.AllSettled() {
				run.Status = RunCompleted
			} else {
				run.Status = RunBlocked
				run.Err = ErrDeadlock
			}
			break
		}

		// pending/stale -> ready 必须在调度前显式完成：
		// stale 先回 pending 再进 ready，保证重新执行前状态语义完整。
		for _, step := range layer {
			if step.Status == StatusStale {
				if err := step.Transition(StatusPending); err != nil {
					run.Status = RunFailed
					run.Err = err
					return run.finish(), err
				}
				step.StaleReason = ""
			}
			if err := step.Transition(StatusReady); err != nil {
				run.Status = RunFailed
				run.Err = err
				return run.finish(), err
			}
		}

		prior := FormatPrior(plan.Results())
		budgetHit := p.runLayer(ctx, layer, prior, exec, budget, emitter, cfg, run.RunID)
		if err := ctx.Err(); err != nil {
			run.Status = RunFailed
			run.Err = err
			break
		}

		seeds, reasons := collectInvalidations(plan, layer, opts.Warnf)
		stale := plan.Propagate(seeds)

		if len(seeds) == 0 && plan.AllDone() {
			run.Status = RunCompleted
			break
		}
		if budgetHit || budget.Check() != nil {
			markStale(plan, stale, reasons, emitter)
			run.Status = RunBudgetExhausted
			run.Err = ErrBudgetExhausted
			break
		}
		if len(seeds) == 0 {
			// 本层全部成功且下游可能就绪，继续下一层；
			// 若下一层为空会走 AllSettled / ErrDeadlock 分支。
			continue
		}
		if run.Replans >= cfg.MaxReplans || !budget.TryReserve(1) {
			markStale(plan, stale, reasons, emitter)
			run.Status = RunDegraded
			break
		}

		markStale(plan, stale, reasons, emitter)
		emitter.emit(ProgressEvent{
			Type:    EventReplan,
			Message: fmt.Sprintf("正在根据 %d 个失效步骤修订计划...\n", len(stale)),
		})
		patch, err := p.ReplanPatch(ctx, plan, stale, reasonsFor(stale, reasons), budget)
		if err != nil {
			run.Err = err
			run.Status = RunDegraded
			break
		}
		if err := plan.ApplyPatch(patch); err != nil {
			run.Err = err
			run.Status = RunDegraded
			break
		}
		if err := validatePlan(plan, cfg.MaxSteps); err != nil {
			run.Err = err
			run.Status = RunDegraded
			break
		}
		plan.Revision++
		run.Replans++
		plan.ResetStale()
		emitter.emit(ProgressEvent{Type: EventPlan, Message: plan.FormatPlan()})
	}

	emitter.emit(ProgressEvent{Type: EventDone, Message: string(run.Status)})
	run.finish()
	if run.Status == RunBlocked {
		return run, ErrDeadlock
	}
	if run.Status == RunBudgetExhausted {
		return run, run.Err
	}
	return run, nil
}

func (r *RunResult) finish() *RunResult {
	if r == nil {
		return nil
	}
	r.Results = r.Plan.Results()
	r.Duration = time.Since(r.StartedAt)
	return r
}

// markStale 把已产出结果的失效步骤标为 stale。
// 只标记 done/failed：pending 还没跑过，没有结果可作废。
func markStale(plan *Plan, ids []int, reasons map[int]string, emitter *eventEmitter) {
	for _, id := range ids {
		reason := reasons[id]
		if reason == "" {
			reason = "上游步骤结果失效"
		}
		step := plan.Step(id)
		if step == nil {
			continue
		}
		if step.Status != StatusDone && step.Status != StatusFailed {
			continue
		}
		step.Status = StatusStale
		step.StaleReason = reason
		emitter.emit(ProgressEvent{Type: EventStale, StepID: id, Message: reason})
	}
}

// runLayer 执行一层：只读步骤并发，副作用步骤排在其后串行。
func (p *Planner) runLayer(ctx context.Context, layer []*PlanStep, prior string, exec StepExecutorV2,
	budget *Budget, emitter *eventEmitter, cfg Config, runID string) bool {

	parallel := make([]*PlanStep, 0, len(layer))
	serial := make([]*PlanStep, 0, len(layer))
	for _, step := range layer {
		if step.Effect == EffectSideEffect {
			serial = append(serial, step)
		} else {
			parallel = append(parallel, step)
		}
	}

	var budgetHit atomic.Bool
	if len(parallel) > 0 {
		limit := cfg.MaxConcurrency
		if limit > len(parallel) {
			limit = len(parallel)
		}
		sem := make(chan struct{}, limit)
		var wg sync.WaitGroup
		for _, step := range parallel {
			wg.Add(1)
			go func(step *PlanStep) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				if !p.runStep(ctx, step, prior, exec, budget, emitter, cfg, runID) {
					budgetHit.Store(true)
				}
			}(step)
		}
		wg.Wait()
	}
	for _, step := range serial {
		if !p.runStep(ctx, step, prior, exec, budget, emitter, cfg, runID) {
			budgetHit.Store(true)
			break
		}
	}
	return budgetHit.Load()
}

// runStep 执行单步并维护状态机。返回 false 表示预算不足、未能执行。
func (p *Planner) runStep(ctx context.Context, step *PlanStep, prior string, exec StepExecutorV2,
	budget *Budget, emitter *eventEmitter, cfg Config, runID string) bool {

	if ctx.Err() != nil {
		return true
	}
	if !budget.TryReserve(1) {
		return false
	}
	if err := step.Transition(StatusRunning); err != nil {
		step.err = err
		// 状态机异常不能把步骤留在 ready，否则下一轮 ReadyLayer 会再次选中它，
		// 形成"调度空转"——比直接失败更难排查。
		step.Status = StatusFailed
		return true
	}
	step.Attempt = 0
	step.err = nil

	start := time.Now()
	var outcome StepOutcome
	var err error
	for attempt := 1; attempt <= cfg.MaxRetries+1; attempt++ {
		if attempt > 1 && !budget.TryReserve(1) {
			break
		}
		step.Attempt = attempt
		key := IdempotencyKey(runID, step.ID, step.Tool, []byte(step.Description))
		stepCtx := WithIdempotencyKey(ctx, key)
		emitter.emit(ProgressEvent{
			Type: EventStepStart, StepID: step.ID, Attempt: attempt, Message: step.Description,
		})
		outcome, err = exec(stepCtx, *step, prior)
		if err == nil {
			if strings.TrimSpace(outcome.Output) == "" && len(outcome.Facts) == 0 {
				err = ErrEmptyStepOutput
			}
		}
		if err == nil || ClassifyError(err) != FailTransient || attempt > cfg.MaxRetries {
			break
		}
		if !sleepBackoff(ctx, cfg.RetryBase, attempt) {
			break
		}
	}
	step.duration = time.Since(start)

	if err != nil {
		step.err = err
		step.Output = outcome.Output
		if strings.TrimSpace(step.Output) == "" {
			step.Output = err.Error()
		}
		_ = step.Transition(StatusFailed)
		emitter.emit(ProgressEvent{
			Type: EventStepFailed, StepID: step.ID, Attempt: step.Attempt,
			Message: truncateBytes(err.Error(), 512),
		})
		return true
	}

	step.Output = outcome.Output
	step.outcome = outcome
	_ = step.Transition(StatusDone)
	emitter.emit(ProgressEvent{Type: EventStepDone, StepID: step.ID, Attempt: step.Attempt})
	return true
}

// collectInvalidations 汇总失败步骤与被 violation 指向的步骤。
//
// 模型编造 step_id 的概率远高于真的发现矛盾，单条非法 violation 只忽略并告警，
// 不因此放弃整轮已经完成的工作。
func collectInvalidations(plan *Plan, layer []*PlanStep, warnf func(string, ...any)) ([]int, map[int]string) {
	seen := make(map[int]bool)
	reasons := make(map[int]string)
	add := func(id int, reason string) {
		if id <= 0 || plan.Step(id) == nil || seen[id] {
			return
		}
		seen[id] = true
		reasons[id] = reason
	}
	for _, step := range layer {
		if step.Status == StatusFailed {
			reason := step.err
			text := "步骤执行失败"
			if reason != nil {
				text = reason.Error()
			}
			add(step.ID, text)
		}
		for _, v := range step.outcome.Violations {
			if v.StepID <= 0 || plan.Step(v.StepID) == nil {
				if warnf != nil {
					warnf("忽略指向不存在步骤的 violation: step=%d target=%d", step.ID, v.StepID)
				}
				continue
			}
			if v.StepID == step.ID {
				if warnf != nil {
					warnf("忽略步骤自指的 violation: step=%d", step.ID)
				}
				continue
			}
			reason := v.Reason
			if reason == "" {
				reason = v.Claim
			}
			add(v.StepID, fmt.Sprintf("步骤 %d 报告：%s", step.ID, reason))
		}
	}
	ids := make([]int, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids, reasons
}

func reasonsFor(ids []int, reasons map[int]string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		text := fmt.Sprintf("步骤 %d 失效", id)
		if r := reasons[id]; r != "" {
			text = fmt.Sprintf("步骤 %d：%s", id, r)
		}
		out = append(out, text)
	}
	return out
}

// IdempotencyKey 由 run + step + 逻辑意图决定，与 attempt 无关。
// 同一步骤的重试必须复用同一个 key，否则"重试"会变成"再执行一次副作用"。
func IdempotencyKey(runID string, stepID int, tool string, args []byte) string {
	h := sha256.New()
	h.Write([]byte(runID))
	fmt.Fprintf(h, ":%d:%s:", stepID, tool)
	h.Write(args)
	return hex.EncodeToString(h.Sum(nil))[:32]
}

type idempotencyKeyCtx struct{}

// WithIdempotencyKey 把幂等键下发给工具层。
func WithIdempotencyKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, idempotencyKeyCtx{}, key)
}

// IdempotencyKeyFrom 读取当前步骤的幂等键。
func IdempotencyKeyFrom(ctx context.Context) (string, bool) {
	key, ok := ctx.Value(idempotencyKeyCtx{}).(string)
	return key, ok && key != ""
}

func sleepBackoff(ctx context.Context, base time.Duration, attempt int) bool {
	backoff := base
	for i := 1; i < attempt; i++ {
		backoff *= 2
	}
	// ±20% 抖动，避免同一层的多个步骤在同一时刻一起重试。
	backoff = time.Duration(float64(backoff) * (0.8 + 0.4*rand.Float64()))
	timer := time.NewTimer(backoff)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func newRunID() string {
	var buf [16]byte
	if _, err := cryptorand.Read(buf[:]); err != nil {
		return fmt.Sprintf("run-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf[:])
}

const replanPatchSystemPrompt = `你是任务规划修补器。原计划执行中出现了失败或前提被推翻，需要增量修订。
规则：
1. 只输出对现有计划的 patch，不要输出完整新计划，禁止 reset / 清空重来
2. operations 支持 add / modify / remove / replace_from
3. add 与 replace_from 的新步骤必须带本次 patch 内唯一的 local_id；全局 id 由系统分配
4. 新步骤的 depends_on 可以引用已有步骤的数字 id，也可以引用本次 patch 的 local_id
5. modify 只能改 description / tool / assumptions / expects / depends_on，不允许改 effect
6. 保留仍然有效、且未受影响的步骤，不要为了"看起来整齐"重写整条链
输出 JSON，不要输出其他内容：
{"reasoning":"为什么这样修","operations":[
  {"op":"replace_from","id":2,"steps":[
    {"local_id":"a","description":"用 XLSX 解析器重新读取文件","depends_on":[],"effect":"none"}]}]}`

// ReplanPatch 让模型输出增量 patch，而不是重写整份计划。
//
// prompt 必须带预算：模型不知道剩余调用数时倾向于生成更长的修复链，
// 这会把"局部修复"重新变成一次全量规划。
func (p *Planner) ReplanPatch(ctx context.Context, plan *Plan, stale []int,
	reasons []string, budget *Budget) (*PlanPatch, error) {

	if plan == nil {
		return nil, fmt.Errorf("计划为空")
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("原始目标（不可修改）：%s\n\n", plan.Goal))
	sb.WriteString(fmt.Sprintf("当前计划（revision %d）：\n", plan.Revision))
	results := plan.Results()
	for _, r := range results {
		line := fmt.Sprintf("  [%d] %-8s %s", r.Step.ID, r.Step.Status, r.Step.Description)
		switch {
		case r.Stale:
			line += "  失效原因：" + r.Step.StaleReason
		case r.Success && strings.TrimSpace(r.Output) != "":
			line += "  结果摘要：" + truncateBytes(strings.ReplaceAll(r.Output, "\n", " "), 300)
		case r.Err != nil:
			line += "  失败原因：" + truncateBytes(r.Err.Error(), 300)
		}
		sb.WriteString(line + "\n")
	}

	var facts []string
	for _, r := range results {
		if r.Stale {
			continue
		}
		for _, f := range r.Outcome.Facts {
			facts = append(facts, fmt.Sprintf("  %s = %s", f.Key, truncateBytes(f.Value, 200)))
			if len(facts) >= 20 {
				break
			}
		}
	}
	if len(facts) > 0 {
		sb.WriteString("\n已知事实：\n")
		sb.WriteString(strings.Join(facts, "\n") + "\n")
	}

	sb.WriteString(fmt.Sprintf("\n失效集合：%v\n", stale))
	if len(reasons) > 0 {
		sb.WriteString("失效原因：\n")
		for _, reason := range reasons {
			sb.WriteString("  - " + reason + "\n")
		}
	}
	if budget != nil {
		sb.WriteString(fmt.Sprintf("可用预算：剩余 %d 次模型调用、%.0f 秒\n",
			budget.Remaining(), budget.RemainingWall().Seconds()))
	}
	sb.WriteString("\n请输出修订当前计划的 patch JSON。")

	resp, err := p.chat(ctx, replanPatchSystemPrompt, sb.String())
	if err != nil {
		return nil, fmt.Errorf("重规划 LLM 调用失败: %w", err)
	}
	return ParsePlanPatch(resp)
}

// eventEmitter 用单 goroutine 汇聚事件，保证并发步骤的回调不会交错。
type eventEmitter struct {
	ch chan ProgressEvent
	wg sync.WaitGroup
}

func newEmitter(listener ProgressListenerV2) *eventEmitter {
	if listener == nil {
		return nil
	}
	e := &eventEmitter{ch: make(chan ProgressEvent, 64)}
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		for ev := range e.ch {
			listener(ev)
		}
	}()
	return e
}

// emit 队列满时丢弃 step_start，保留 done / failed / stale / replan。
// 进度可以丢，状态不能丢。
func (e *eventEmitter) emit(ev ProgressEvent) {
	if e == nil {
		return
	}
	if ev.At.IsZero() {
		ev.At = time.Now()
	}
	select {
	case e.ch <- ev:
		return
	default:
	}
	if ev.Type == EventStepStart {
		return
	}
	e.ch <- ev
}

func (e *eventEmitter) Close() {
	if e == nil {
		return
	}
	close(e.ch)
	e.wg.Wait()
}
