package planner

import (
	"context"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"Logos/internal/bot/toolerr"
	"Logos/pkg/obs"
	"Logos/pkg/strutil"
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
	ErrToolNotFound = toolerr.ErrToolNotFound
	// ErrInvalidArgs 工具参数非法，重试没有意义。
	ErrInvalidArgs = toolerr.ErrInvalidArgs
	// ErrPermission 权限不足，重试没有意义。
	ErrPermission = toolerr.ErrPermission
	// ErrRateLimited 上游限流，属于可重试的瞬时失败。
	ErrRateLimited = toolerr.ErrRateLimited
	// ErrTransient 供工具层包装超时、5xx、连接重置等瞬时错误。
	ErrTransient = toolerr.ErrTransient
	// ErrEmptyStepOutput 步骤成功返回但既无正文也无事实，视为失败。
	// 空字符串如果被当成成功，会进入综合阶段并产生一个"看起来正常"的空答案。
	ErrEmptyStepOutput = errors.New("empty step output")
	// ErrAssumptionViolated 前置校验判定步骤前提不成立，需要重规划绕开。
	ErrAssumptionViolated = errors.New("assumption violated")
	// ErrExpectationMissed 后置校验判定产出不满足 expects，按失败处理。
	ErrExpectationMissed = errors.New("expectation missed")
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
	if toolerr.IsTransient(err) {
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
	// MaxTokens 本轮允许的 token 预算（按字节估算；供应商回传 usage 时优先用真实值）。
	MaxTokens int
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
		MaxTokens:      150_000,
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
	if c.MaxTokens <= 0 {
		c.MaxTokens = def.MaxTokens
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
	maxCalls  int
	maxTokens int
	maxWall   time.Duration
	start     time.Time
	used      atomic.Int64
	tokens    atomic.Int64
}

// NewBudget 按配置创建预算。
func NewBudget(cfg Config) *Budget {
	cfg = cfg.withDefaults()
	return &Budget{
		maxCalls:  cfg.MaxLLMCalls,
		maxTokens: cfg.MaxTokens,
		maxWall:   cfg.MaxWallClock,
		start:     time.Now(),
	}
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

// Release 退还尚未使用的预留额度。
//
// 层内并发会一次性预留整层调用数，但取消、状态机拒绝等路径可能让某个
// 步骤根本没开始执行；不退还会把预算"漏"在一个永远不会发生的调用上。
func (b *Budget) Release(n int) {
	if b == nil || n <= 0 {
		return
	}
	for {
		cur := b.used.Load()
		next := cur - int64(n)
		if next < 0 {
			next = 0
		}
		if b.used.CompareAndSwap(cur, next) {
			return
		}
	}
}

// TryReserveTokens 预留 token 预算。
// 在调用模型前按 prompt 估算预留，拿到响应后再用 AddTokens 补差额：
// 只在调用后记账的话，超支已经发生，预算就变成了"事后统计"。
func (b *Budget) TryReserveTokens(n int) bool {
	if b == nil || n <= 0 {
		return true
	}
	for {
		cur := b.tokens.Load()
		if cur+int64(n) > int64(b.maxTokens) {
			return false
		}
		if b.tokens.CompareAndSwap(cur, cur+int64(n)) {
			return true
		}
	}
}

// AddTokens 追加实际消耗（响应内容）。
func (b *Budget) AddTokens(n int) {
	if b == nil || n <= 0 {
		return
	}
	b.tokens.Add(int64(n))
}

// ReleaseTokens 退还尚未真正发生的 token 预留。
// 只有"预留成功但调用没发出去"的路径才需要退：例如状态机拒绝、
// 重试前发现预算不足。已经发出的调用不能退，否则预算会低估实际成本。
func (b *Budget) ReleaseTokens(n int) {
	if b == nil || n <= 0 {
		return
	}
	for {
		cur := b.tokens.Load()
		next := cur - int64(n)
		if next < 0 {
			next = 0
		}
		if b.tokens.CompareAndSwap(cur, next) {
			return
		}
	}
}

// Used 返回已消耗的调用次数。
func (b *Budget) Used() int {
	if b == nil {
		return 0
	}
	return int(b.used.Load())
}

// TokensUsed 返回已消耗的 token 估算值。
func (b *Budget) TokensUsed() int {
	if b == nil {
		return 0
	}
	return int(b.tokens.Load())
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

// RemainingTokens 返回剩余 token 预算。
func (b *Budget) RemainingTokens() int {
	if b == nil {
		return 0
	}
	left := int64(b.maxTokens) - b.tokens.Load()
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
	if b.RemainingTokens() <= 0 {
		return fmt.Errorf("%w: token 预算已用尽", ErrBudgetExhausted)
	}
	if b.RemainingWall() <= 0 {
		return fmt.Errorf("%w: 执行时间已用尽", ErrBudgetExhausted)
	}
	return nil
}

// ExhaustedKind 返回预算耗尽的具体维度，供指标区分"次数/token/墙钟"。
// 不返回空串：调用方拿不到原因时，指标会退化成一条无法定位的告警。
func (b *Budget) ExhaustedKind() string {
	if b == nil {
		return ""
	}
	switch {
	case b.Remaining() <= 0:
		return "calls"
	case b.RemainingTokens() <= 0:
		return "tokens"
	case b.RemainingWall() <= 0:
		return "wallclock"
	default:
		return "unknown"
	}
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

// StepReport 单步收口快照，供服务层打一条结构化日志。
//
// 指标只有聚合值，排障时仍然要能回答"这一轮的第 3 步为什么慢/为什么失败"；
// 把 step_id、attempt、effect、fail_kind 放在同一行，日志平台才能直接按
// run_id 串起一次执行。
type StepReport struct {
	RunID       string
	Revision    int
	StepID      int
	Attempt     int
	Status      StepStatus
	Effect      EffectLevel
	FailKind    string
	StaleReason string
	Duration    time.Duration
	TokensIn    int
	TokensOut   int
	Replayed    bool
}

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
	// OnStep 每个步骤收口时回调一次；用于结构化日志与外部审计。
	// 并发层会从多个 goroutine 同时调用，回调实现必须自行保证线程安全。
	OnStep func(StepReport)
	// Warnf 记录可恢复异常（例如模型编造了不存在的 violation step_id）。
	Warnf func(format string, args ...any)
	// Budget 与规划/综合共享的预算；为 nil 时按 Config 新建。
	Budget *Budget
	// Store 运行快照与步骤 checkpoint；为 nil 时不持久化。
	Store RunStore
	// Meta 标识这次运行归属，并携带可选的续跑 run_id。
	Meta RunMeta
	// VerifyAssumes 执行前校验 assumptions（路径 B）。
	VerifyAssumes bool
	// VerifyExpects 执行后校验 expects（路径 C）。
	VerifyExpects bool
}

// RunResult 整轮执行结果。
type RunResult struct {
	RunID      string
	Status     RunStatus
	Plan       *Plan
	Results    []StepResult
	Replans    int
	Revision   int
	LLMCalls   int
	TokensUsed int
	StaleSteps int
	// ReplayedSteps 命中华副作用重放、未真正调用工具的步骤数。
	ReplayedSteps int
	StartedAt     time.Time
	Duration      time.Duration
	Err           error

	// budget 只用于收口统计，不参与序列化。
	budget *Budget
}

// execState 是执行期共享上下文：把 run_id / 当前 revision / 存储接口
// 收在一个结构里，避免每个步骤函数都传一长串参数。
type execState struct {
	runID    string
	revision int
	store    RunStore
	warnf    func(format string, args ...any)

	verifyAssumes bool
	verifyExpects bool
	// verifyCache 以"步骤前提 + 已知事实"为键缓存校验结论，
	// 避免同一层内多个步骤重复问同一个问题。
	verifyCache sync.Map

	replayed atomic.Int64
	observe  func(StepReport)
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
	budget := opts.Budget
	if budget == nil {
		budget = NewBudget(cfg)
	}
	run.budget = budget

	st := &execState{
		runID:         run.RunID,
		revision:      plan.Revision,
		store:         opts.Store,
		warnf:         opts.Warnf,
		observe:       opts.OnStep,
		verifyAssumes: opts.VerifyAssumes,
		verifyExpects: opts.VerifyExpects,
	}
	if st.warnf == nil {
		st.warnf = func(string, ...any) {}
	}

	// 运行快照优先走续跑，其次新建。
	// 续跑失败一律降级为"开一轮新的"，不能因为一条历史记录不可用
	// 就让用户这次请求直接失败。
	if opts.Store != nil {
		resumed := false
		if opts.Meta.ResumeRunID != "" {
			rec, err := opts.Store.GetRun(ctx, opts.Meta.ResumeRunID)
			switch {
			case err != nil:
				st.warnf("读取续跑运行记录失败: %v", err)
			case rec == nil:
				st.warnf("续跑运行记录不存在: %s", opts.Meta.ResumeRunID)
			case resumeRejectReason(rec, plan, opts.Meta) != "":
				st.warnf("忽略续跑请求: %s", resumeRejectReason(rec, plan, opts.Meta))
			default:
				steps, err := opts.Store.ListSteps(ctx, rec.ID)
				if err != nil {
					st.warnf("读取续跑步骤失败: %v", err)
					break
				}
				hydrated, err := hydrateResume(rec, steps)
				if err != nil {
					st.warnf("续跑计划恢复失败: %v", err)
					break
				}
				plan = hydrated
				run.Plan = plan
				run.RunID = rec.ID
				st.runID = rec.ID
				st.revision = plan.Revision
				resumed = true
				status := RunRunning
				if err := opts.Store.UpdateRun(ctx, rec.ID,
					RunPatch{Status: &status}); err != nil {
					st.warnf("续跑状态更新失败: %v", err)
				}
			}
		}
		if !resumed {
			st.revision = plan.Revision
			rec := &RunRecord{
				ID:             run.RunID,
				BotID:          opts.Meta.BotID,
				ConversationID: opts.Meta.ConversationID,
				UserID:         opts.Meta.UserID,
				Goal:           plan.Goal,
				GoalHash:       GoalHash(plan.Goal),
				Status:         RunRunning,
				Revision:       plan.Revision,
				Plan:           plan.DeepCopy(),
				CreatedAt:      run.StartedAt,
				UpdatedAt:      run.StartedAt,
			}
			if err := opts.Store.CreateRun(ctx, rec); err != nil {
				run.Status = RunFailed
				run.Err = fmt.Errorf("创建运行记录失败: %w", err)
				// 运行记录没建成，后续 UpdateRun 一定失败；这里只收口内存结果与指标。
				return run.finalize(ctx, nil, st), run.Err
			}
		}
	}

	emitter := newEmitter(opts.Listener)
	if emitter != nil {
		defer emitter.Close()
	}
	emitter.emit(ProgressEvent{Type: EventPlan, Message: plan.FormatPlan()})

	if err := ctx.Err(); err != nil {
		run.Status = RunFailed
		run.Err = err
		return run.finalize(ctx, opts.Store, st), err
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
					break
				}
				step.StaleReason = ""
			}
			if err := step.Transition(StatusReady); err != nil {
				run.Status = RunFailed
				run.Err = err
				break
			}
		}
		if run.Status == RunFailed {
			break
		}

		prior := FormatPrior(plan.Results())
		budgetHit := p.runLayer(ctx, layer, prior, exec, budget, emitter, cfg, st)
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
			if err := markStale(plan, stale, reasons, emitter); err != nil {
				run.Status = RunFailed
				run.Err = err
				break
			}
			run.Status = RunBudgetExhausted
			run.Err = ErrBudgetExhausted
			break
		}
		if len(seeds) == 0 {
			// 本层全部成功且下游可能就绪，继续下一层；
			// 若下一层为空会走 AllSettled / ErrDeadlock 分支。
			continue
		}
		// 不在这里扣减调用次数：ReplanPatch 走 chatWithBudget，
		// 由它同时预留调用数与 token；否则 patch 调用会绕过 token 预算。
		if run.Replans >= cfg.MaxReplans || budget.Check() != nil {
			if err := markStale(plan, stale, reasons, emitter); err != nil {
				run.Status = RunFailed
				run.Err = err
				break
			}
			run.Status = RunDegraded
			break
		}

		obs.RecordPlannerReplan(ctx, replanReason(layer))
		if err := markStale(plan, stale, reasons, emitter); err != nil {
			run.Status = RunFailed
			run.Err = err
			break
		}
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
		affected, err := plan.ApplyPatchTracked(patch)
		if err != nil {
			run.Err = err
			run.Status = RunDegraded
			break
		}
		if err := validatePlan(plan, cfg.MaxSteps); err != nil {
			run.Err = err
			run.Status = RunDegraded
			break
		}
		// patch 允许修改已完成的步骤（描述/工具/前提/预期/依赖）。
		// 这些字段变了，旧结果就不再对应新的步骤定义，必须连同下游一起重跑；
		// 只依赖"失效集合"无法覆盖这种情况，因为被修改的步骤可能根本不在集合里。
		if stale := plan.Propagate(affected); len(stale) > 0 {
			reasons := make(map[int]string, len(affected))
			for _, id := range affected {
				reasons[id] = "步骤定义被 patch 修改"
			}
			if err := markStale(plan, stale, reasons, emitter); err != nil {
				run.Status = RunFailed
				run.Err = err
				break
			}
		}
		plan.Revision++
		run.Replans++
		st.revision = plan.Revision
		if opts.Store != nil {
			rev := plan.Revision
			if err := opts.Store.UpdateRun(ctx, run.RunID, RunPatch{
				Revision: &rev,
				Plan:     plan.DeepCopy(),
			}); err != nil {
				// 快照写不进去时继续执行，续跑会拿到旧计划结构，
				// 这比"本轮少一次重规划"更危险，直接按降级收口。
				st.warnf("计划快照写入失败: run=%s revision=%d err=%v", run.RunID, rev, err)
				run.Status = RunDegraded
				run.Err = fmt.Errorf("计划快照写入失败: %w", err)
				break
			}
		}
		plan.ResetStale()
		emitter.emit(ProgressEvent{Type: EventPlan, Message: plan.FormatPlan()})
	}

	emitter.emit(ProgressEvent{Type: EventDone, Message: string(run.Status)})
	run.finalize(ctx, opts.Store, st)
	if run.Status == RunBlocked {
		return run, ErrDeadlock
	}
	if run.Status == RunBudgetExhausted {
		return run, run.Err
	}
	return run, nil
}

// finalize 统一收口：统计、终态落库、指标。
//
// 所有退出路径都必须经过这里。分散写终态最容易漏掉 ctx 取消这类路径，
// 结果就是运行记录永远停在 running，续跑逻辑再也不会选中它。
func (r *RunResult) finalize(ctx context.Context, store RunStore, st *execState) *RunResult {
	if r == nil {
		return nil
	}
	if st != nil {
		r.ReplayedSteps = int(st.replayed.Load())
	}
	r.finish()
	if store != nil && r.RunID != "" {
		finalCtx := ctx
		if finalCtx == nil {
			finalCtx = context.Background()
		}
		if finalCtx.Err() != nil {
			// ctx 已经被取消时仍然要落终态，否则记录会永远停在 running。
			finalCtx = context.WithoutCancel(finalCtx)
		}
		status := r.Status
		errText := ""
		if r.Err != nil {
			errText = r.Err.Error()
		}
		rev := r.Revision
		if err := store.UpdateRun(finalCtx, r.RunID, RunPatch{
			Status: &status, Revision: &rev, Error: &errText,
		}); err != nil {
			if st != nil {
				st.warnf("运行终态写入失败: run=%s err=%v", r.RunID, err)
			}
		}
	}
	recordRunMetrics(ctx, r)
	return r
}

func (r *RunResult) finish() *RunResult {
	if r == nil {
		return nil
	}
	if r.Plan != nil {
		r.Results = r.Plan.Results()
		r.Revision = r.Plan.Revision
	}
	r.Duration = time.Since(r.StartedAt)
	if r.budget != nil {
		r.LLMCalls = r.budget.Used()
		r.TokensUsed = r.budget.TokensUsed()
	}
	r.StaleSteps = 0
	for _, res := range r.Results {
		if res.Stale || res.Step.Status == StatusStale {
			r.StaleSteps++
		}
	}
	return r
}

// resumeRejectReason 返回拒绝续跑的原因；空串表示可以续跑。
// 归属校验只在调用方提供了对应字段时执行，避免后台任务（无 user_id）
// 因为"空不等于空"被误拒。
func resumeRejectReason(rec *RunRecord, plan *Plan, meta RunMeta) string {
	if rec == nil {
		return "运行记录为空"
	}
	if rec.Plan == nil {
		return "运行记录缺少计划快照"
	}
	if rec.Status != RunRunning && rec.Status != RunFailed {
		return fmt.Sprintf("状态 %s 不允许续跑", rec.Status)
	}
	if time.Since(rec.UpdatedAt) > ResumeWindow {
		return fmt.Sprintf("超过续跑窗口 %s", ResumeWindow)
	}
	if meta.BotID != "" && rec.BotID != meta.BotID {
		return "Bot 归属不一致"
	}
	if meta.ConversationID != "" && rec.ConversationID != meta.ConversationID {
		return "会话归属不一致"
	}
	if meta.UserID != "" && rec.UserID != meta.UserID {
		return "用户归属不一致"
	}
	if rec.GoalHash == "" || rec.GoalHash != GoalHash(plan.Goal) {
		return "目标已变化"
	}
	return ""
}

// replanReason 给重规划指标一个固定枚举标签，不拼接步骤 ID。
func replanReason(layer []*PlanStep) string {
	for _, step := range layer {
		if step.Status == StatusFailed {
			return "failure"
		}
	}
	return "violation"
}

// recordRunMetrics 记录整轮结果。
// 标签只用固定枚举，run_id / goal 等高基数字段留在日志里。
func recordRunMetrics(ctx context.Context, run *RunResult) {
	if run == nil {
		return
	}
	obs.RecordPlannerRun(ctx, string(run.Status), run.LLMCalls)
	if run.StaleSteps > 0 {
		obs.RecordPlannerStaleSteps(ctx, int64(run.StaleSteps))
	}
	if run.ReplayedSteps > 0 {
		obs.RecordPlannerReplayedSteps(ctx, int64(run.ReplayedSteps))
	}
	if run.Status == RunBudgetExhausted && run.budget != nil {
		obs.RecordPlannerBudgetExhausted(ctx, run.budget.ExhaustedKind())
	}
}

// appendStepCheckpoint 写步骤 checkpoint。
// 持久化失败不能中断执行：checkpoint 是"下一次能续跑"的增强，
// 不是本轮正确性的前提；但必须告警，否则会静默退化成没有 checkpoint。
func (st *execState) appendStepCheckpoint(ctx context.Context, rec *StepRecord) {
	if st == nil || st.store == nil || rec == nil {
		return
	}
	if err := st.store.AppendStep(ctx, rec); err != nil {
		st.warnf("步骤 checkpoint 写入失败: run=%s step=%d attempt=%d err=%v",
			rec.RunID, rec.StepID, rec.Attempt, err)
	}
}

func (st *execState) checkpointStep(ctx context.Context, step *PlanStep, inputHash, prior string,
	attempt int, startedAt time.Time, err error) {
	if st == nil || st.store == nil || step == nil {
		return
	}
	rec := &StepRecord{
		ID:         newRunID(),
		RunID:      st.runID,
		StepID:     step.ID,
		Revision:   st.revision,
		Attempt:    attempt,
		Status:     step.Status,
		Effect:     step.Effect,
		InputHash:  inputHash,
		Output:     step.Output,
		Facts:      append([]Fact(nil), step.outcome.Facts...),
		Violations: append([]Violation(nil), step.outcome.Violations...),
		// 供应商没有回传 usage 时，用与预算口径一致的估算值留痕：
		// 至少能回答"这一步大概烧了多少"，而不是所有步骤都显示 0。
		TokensIn:   EstimateTokens(BuildStepPrompt(*step, prior)),
		TokensOut:  EstimateTokens(step.Output),
		DurationMs: step.duration.Milliseconds(),
		StartedAt:  startedAt,
		FinishedAt: time.Now(),
	}
	if err != nil {
		rec.Error = truncateBytes(err.Error(), 2048)
	}
	st.appendStepCheckpoint(ctx, rec)
}

// reportStep 在步骤收口时生成结构化快照。
// 没有 store 也要能观测，因此不依赖 checkpoint 是否开启。
func (st *execState) reportStep(step *PlanStep, prior string, replayed bool) {
	if st == nil || st.observe == nil || step == nil {
		return
	}
	st.reportStepTokens(step,
		EstimateTokens(BuildStepPrompt(*step, prior)),
		EstimateTokens(step.Output), replayed)
}

func (st *execState) reportStepTokens(step *PlanStep, tokensIn, tokensOut int, replayed bool) {
	if st == nil || st.observe == nil || step == nil {
		return
	}
	st.observe(StepReport{
		RunID:       st.runID,
		Revision:    st.revision,
		StepID:      step.ID,
		Attempt:     step.Attempt,
		Status:      step.Status,
		Effect:      step.Effect,
		FailKind:    failKindLabel(step.err),
		StaleReason: step.StaleReason,
		Duration:    step.duration,
		TokensIn:    tokensIn,
		TokensOut:   tokensOut,
		Replayed:    replayed,
	})
}

// failKindLabel 给日志一个稳定枚举，不把错误文本当标签。
func failKindLabel(err error) string {
	if err == nil {
		return ""
	}
	switch {
	case errors.Is(err, ErrAssumptionViolated), errors.Is(err, ErrExpectationMissed):
		return "validation"
	case ClassifyError(err) == FailTransient:
		return "transient"
	default:
		return "permanent"
	}
}

// markStale 把已产出结果的失效步骤标为 stale。
// 只标记 done/failed：pending 还没跑过，没有结果可作废。
// 状态迁移统一走 Transition，避免某条新路径绕过状态机后留下无法调度的状态。
func markStale(plan *Plan, ids []int, reasons map[int]string, emitter *eventEmitter) error {
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
		if err := step.Transition(StatusStale); err != nil {
			return err
		}
		step.StaleReason = reason
		emitter.emit(ProgressEvent{Type: EventStale, StepID: id, Message: reason})
	}
	return nil
}

// runLayer 执行一层：只读步骤并发，副作用步骤排在其后串行。
func (p *Planner) runLayer(ctx context.Context, layer []*PlanStep, prior string, exec StepExecutorV2,
	budget *Budget, emitter *eventEmitter, cfg Config, st *execState) bool {

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
		// 并发前先整层预留：预留不到就降级串行，避免"前两步抢到额度、
		// 后两步在 goroutine 里才发现没钱"造成半层执行与额外重试。
		charged := false
		if len(parallel) > 1 {
			if budget.TryReserve(len(parallel)) {
				charged = true
			} else {
				limit = 1
			}
		}
		sem := make(chan struct{}, limit)
		var wg sync.WaitGroup
		for _, step := range parallel {
			wg.Add(1)
			go func(step *PlanStep) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				if !p.runStep(ctx, step, prior, exec, budget, emitter, cfg, st, charged) {
					budgetHit.Store(true)
				}
			}(step)
		}
		wg.Wait()
	}
	for _, step := range serial {
		if !p.runStep(ctx, step, prior, exec, budget, emitter, cfg, st, false) {
			budgetHit.Store(true)
			break
		}
	}
	return budgetHit.Load()
}

// runStep 执行单步并维护状态机。返回 false 表示预算不足、未能执行。
// charged 为 true 表示调用方已完成层内预占，本步首次 attempt 不再重复扣减。
func (p *Planner) runStep(ctx context.Context, step *PlanStep, prior string, exec StepExecutorV2,
	budget *Budget, emitter *eventEmitter, cfg Config, st *execState, charged bool) bool {

	if ctx.Err() != nil {
		if charged {
			budget.Release(1)
		}
		return true
	}
	inputHash := StepInputHash(*step, prior)
	// 单步 Agent 内部可能走多轮 ReAct，规划器拿不到供应商 usage；
	// 这里至少把本步 prompt 按统一口径计入共享预算。否则 MaxTokens 只统计
	// 规划/重规划/综合，执行才是成本大头却完全不设防。
	promptTokens := EstimateTokens(BuildStepPrompt(*step, prior))

	// 副作用重放：同一次运行里，输入指纹一致且已经成功的副作用不再执行。
	// 检查放在预算扣减之前——重放是读历史，不应该消耗本轮调用配额。
	if st != nil && st.store != nil && step.Effect == EffectSideEffect {
		rec, err := st.store.FindStepOutput(ctx, st.runID, step.ID, inputHash)
		if err != nil {
			st.warnf("查询副作用重放记录失败: run=%s step=%d err=%v", st.runID, step.ID, err)
		} else if rec != nil {
			if err := step.Transition(StatusRunning); err != nil {
				step.err = err
				step.Status = StatusFailed
				st.reportStep(step, prior, true)
				return true
			}
			step.Attempt = rec.Attempt
			step.Output = rec.Output
			step.outcome = StepOutcome{
				Output:     rec.Output,
				Facts:      append([]Fact(nil), rec.Facts...),
				Violations: append([]Violation(nil), rec.Violations...),
			}
			step.duration = time.Duration(rec.DurationMs) * time.Millisecond
			_ = step.Transition(StatusDone)
			st.replayed.Add(1)
			obs.RecordPlannerStep(ctx, string(StatusDone), string(step.Effect), step.duration.Seconds())
			emitter.emit(ProgressEvent{
				Type: EventStepDone, StepID: step.ID, Attempt: rec.Attempt,
				Message: "副作用已执行过，复用历史结果",
			})
			st.reportStepTokens(step, rec.TokensIn, rec.TokensOut, true)
			return true
		}
	}

	// charged 表示调用方已按整层预留过本次调用；只有重试才需要额外扣减。
	if !charged && !budget.TryReserve(1) {
		return false
	}
	if !budget.TryReserveTokens(promptTokens) {
		// 调用没发出去，调用额度必须同步退回。
		budget.Release(1)
		return false
	}
	if err := step.Transition(StatusRunning); err != nil {
		// 预留了但一次模型/工具都没调，额度必须退回，否则会挤压后续步骤。
		budget.Release(1)
		budget.ReleaseTokens(promptTokens)
		step.err = err
		// 状态机异常不能把步骤留在 ready，否则下一轮 ReadyLayer 会再次选中它，
		// 形成"调度空转"——比直接失败更难排查。
		step.Status = StatusFailed
		st.reportStep(step, prior, false)
		return true
	}
	step.Attempt = 0
	step.err = nil

	start := time.Now()
	if st != nil && st.store != nil {
		st.appendStepCheckpoint(ctx, &StepRecord{
			ID:        newRunID(),
			RunID:     st.runID,
			StepID:    step.ID,
			Revision:  st.revision,
			Attempt:   0,
			Status:    StatusRunning,
			Effect:    step.Effect,
			InputHash: inputHash,
			StartedAt: start,
		})
	}

	// 路径 B：执行前校验前提。模型说"不成立"才判定失败；
	// 校验调用本身失败按"校验不可用"放行，避免把观测故障变成业务失败。
	if st != nil && st.verifyAssumes && len(step.Assumptions) > 0 {
		if ok, reason := st.checkAssumptions(ctx, p, *step, prior, budget); !ok {
			reason = strings.TrimSpace(reason)
			if reason == "" {
				reason = "步骤前提不成立"
			}
			err := fmt.Errorf("%w: %s", ErrAssumptionViolated, reason)
			step.duration = time.Since(start)
			step.err = err
			step.Output = err.Error()
			_ = step.Transition(StatusFailed)
			// 前提校验已经单独记过预算，本次步骤调用没有发生，退还预留。
			budget.Release(1)
			budget.ReleaseTokens(promptTokens)
			st.checkpointStep(ctx, step, inputHash, prior, 0, start, err)
			obs.RecordPlannerStep(ctx, string(StatusFailed), string(step.Effect), step.duration.Seconds())
			emitter.emit(ProgressEvent{
				Type: EventStepFailed, StepID: step.ID, Attempt: 0,
				Message: truncateBytes(err.Error(), 512),
			})
			st.reportStep(step, prior, false)
			return true
		}
	}

	var outcome StepOutcome
	var err error
	for attempt := 1; attempt <= cfg.MaxRetries+1; attempt++ {
		if attempt > 1 {
			if !budget.TryReserve(1) {
				break
			}
			if !budget.TryReserveTokens(promptTokens) {
				budget.Release(1)
				break
			}
		}
		step.Attempt = attempt
		key := IdempotencyKey(st.runID, step.ID, step.Tool, []byte(step.Description))
		stepCtx := WithIdempotencyKey(ctx, key)
		emitter.emit(ProgressEvent{
			Type: EventStepStart, StepID: step.ID, Attempt: attempt, Message: step.Description,
		})
		outcome, err = exec(stepCtx, *step, prior)
		// 无论成功失败，Agent 已经产出的正文都按同一估算口径记账。
		budget.AddTokens(EstimateTokens(outcome.Output))
		if err == nil {
			if strings.TrimSpace(outcome.Output) == "" && len(outcome.Facts) == 0 {
				err = ErrEmptyStepOutput
			}
		}
		// 路径 C：只对非副作用步骤做后置校验。
		// 副作用已经发生，校验失败后再跑一次就是重复发送/重复扣款；
		// 这里只记录结果，让综合阶段说明局限。
		if err == nil && st != nil && st.verifyExpects && strings.TrimSpace(step.Expects) != "" {
			if step.Effect == EffectSideEffect {
				st.warnf("跳过副作用步骤的后置校验: run=%s step=%d", st.runID, step.ID)
			} else if ok, reason := st.checkExpects(ctx, p, *step, outcome.Output, budget); !ok {
				reason = strings.TrimSpace(reason)
				if reason == "" {
					reason = "产出不符合预期"
				}
				err = fmt.Errorf("%w: %s", ErrExpectationMissed, reason)
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
		step.outcome = outcome
		step.Output = outcome.Output
		if strings.TrimSpace(step.Output) == "" {
			step.Output = err.Error()
		}
		_ = step.Transition(StatusFailed)
		st.checkpointStep(ctx, step, inputHash, prior, step.Attempt, start, err)
		obs.RecordPlannerStep(ctx, string(StatusFailed), string(step.Effect), step.duration.Seconds())
		emitter.emit(ProgressEvent{
			Type: EventStepFailed, StepID: step.ID, Attempt: step.Attempt,
			Message: truncateBytes(err.Error(), 512),
		})
		st.reportStep(step, prior, false)
		return true
	}

	step.Output = outcome.Output
	step.outcome = outcome
	_ = step.Transition(StatusDone)
	st.checkpointStep(ctx, step, inputHash, prior, step.Attempt, start, nil)
	obs.RecordPlannerStep(ctx, string(StatusDone), string(step.Effect), step.duration.Seconds())
	emitter.emit(ProgressEvent{Type: EventStepDone, StepID: step.ID, Attempt: step.Attempt})
	st.reportStep(step, prior, false)
	return true
}

type verifyResult struct {
	ok     bool
	reason string
}

const verifyAssumptionsPrompt = `你是执行前校验器。判断给定步骤的前提是否成立。
只能依据提供的已知事实；证据不足时按不成立处理，不要臆测。
输出 JSON，不要输出其他内容：
{"holds": true, "reason": "简短理由"}`

const verifyExpectsPrompt = `你是执行结果校验器。判断步骤产出是否满足预期。
只判断"是否达到预期"，不要评价文风或补充答案。
输出 JSON，不要输出其他内容：
{"valid": true, "reason": "简短理由"}`

func (st *execState) checkAssumptions(ctx context.Context, p *Planner,
	step PlanStep, facts string, budget *Budget) (bool, string) {
	key := verifyCacheKey("assume", step, facts)
	if v, ok := st.verifyCache.Load(key); ok {
		r := v.(verifyResult)
		return r.ok, r.reason
	}
	ok, reason := p.verifyAssumptions(ctx, step, facts, budget)
	st.verifyCache.Store(key, verifyResult{ok: ok, reason: reason})
	return ok, reason
}

func (st *execState) checkExpects(ctx context.Context, p *Planner,
	step PlanStep, output string, budget *Budget) (bool, string) {
	key := verifyCacheKey("expects", step, output)
	if v, ok := st.verifyCache.Load(key); ok {
		r := v.(verifyResult)
		return r.ok, r.reason
	}
	ok, reason := p.verifyExpects(ctx, step, output, budget)
	st.verifyCache.Store(key, verifyResult{ok: ok, reason: reason})
	return ok, reason
}

func verifyCacheKey(kind string, step PlanStep, payload string) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\n%d\n%s\n%s\n", kind, step.ID, strings.Join(step.Assumptions, "|"), payload)
	return hex.EncodeToString(h.Sum(nil))
}

// verifyAssumptions 返回 (是否成立, 理由)。
// 解析失败 / 调用失败一律按"校验不可用"放行：校验是可选增强，
// 不能因为校验器自己出错就把整轮任务判死。
func (p *Planner) verifyAssumptions(ctx context.Context, step PlanStep,
	facts string, budget *Budget) (bool, string) {
	if p == nil || p.chat == nil || len(step.Assumptions) == 0 {
		return true, ""
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("步骤：%s\n前提：\n", step.Description))
	for _, a := range step.Assumptions {
		sb.WriteString("- ")
		sb.WriteString(a)
		sb.WriteString("\n")
	}
	if strings.TrimSpace(facts) != "" {
		sb.WriteString("\n已知事实/前序结果：\n")
		sb.WriteString(truncateBytes(facts, 8*1024))
	}
	resp, err := p.chatWithBudget(ctx, verifyAssumptionsPrompt, sb.String(), budget)
	if err != nil {
		return true, ""
	}
	return parseVerifyResponse(resp)
}

func (p *Planner) verifyExpects(ctx context.Context, step PlanStep,
	output string, budget *Budget) (bool, string) {
	if p == nil || p.chat == nil || strings.TrimSpace(step.Expects) == "" {
		return true, ""
	}
	prompt := fmt.Sprintf("步骤：%s\n预期产出：%s\n实际产出：\n%s",
		step.Description, step.Expects, truncateBytes(output, 8*1024))
	resp, err := p.chatWithBudget(ctx, verifyExpectsPrompt, prompt, budget)
	if err != nil {
		return true, ""
	}
	return parseVerifyResponse(resp)
}

func parseVerifyResponse(resp string) (bool, string) {
	var out struct {
		// 兼容两版协议：v2 规格用 holds，早期实现用 valid。
		// 用指针区分"没返回字段"和"返回了 false"：前者是校验不可用，
		// 应放行；后者是明确的"前提不成立"，必须进入重规划。
		Holds  *bool  `json:"holds"`
		Valid  *bool  `json:"valid"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(strutil.ExtractJSON(resp)), &out); err != nil {
		return true, ""
	}
	switch {
	case out.Holds != nil:
		return *out.Holds, out.Reason
	case out.Valid != nil:
		return *out.Valid, out.Reason
	default:
		return true, ""
	}
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

	resp, err := p.chatWithBudget(ctx, replanPatchSystemPrompt, sb.String(), budget)
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
