package tools

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
	"unicode/utf8"

	"Logos/internal/bot/toolbudget"
	"Logos/internal/bot/toolerr"
	"Logos/pkg/logger"
	"Logos/pkg/obs"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// 工具执行层的统一约束。
//
// 工具是模型与外部世界之间唯一有副作用的通道，任何一条工具调用都可能是
// 慢请求、故障请求，或者模型在同一轮里重复发出的相同请求。把这些约束
// 放在每个工具实现里写一遍，必然出现「有的超时、有的不超时」。
const (
	// DefaultToolTimeout 单次工具调用的默认超时。
	// 检索类工具内部各有 2s 级别超时，这里兜底的是「整个调用」，
	// 防止未来接入的外部 MCP 工具无限挂起，拖住整轮对话。
	DefaultToolTimeout = 20 * time.Second
	// DefaultToolConcurrency 并发工具调用上限。
	// 模型一轮最多并行若干工具；不设上限时多个会话叠在一起会把外部服务打满。
	DefaultToolConcurrency = 16
	// toolCacheTTL 只读工具的相同参数结果复用窗口。
	toolCacheTTL = 30 * time.Second
	// toolCacheMaxEntries 缓存条目上限，避免不同查询把内存撑爆。
	toolCacheMaxEntries = 128
	// toolResultRuneLimit 单次工具结果的硬上限（字符）。
	// 各工具自身有预算，这里是最后一道闸：新工具忘记做预算时也不会把
	// 一整页原始 HTML/JSON 塞进 prompt。
	toolResultRuneLimit = 32000

	// defaultRetryAttempts 只读工具在可重试错误上的默认总尝试次数（含首次）。
	// 三次足够覆盖一次网络抖动加一次短暂限流；再多会把一次对话拖进超时。
	defaultRetryAttempts = 3
	// defaultRetryBaseDelay 首次重试前的等待，之后按 2 倍递增并封顶。
	defaultRetryBaseDelay = 100 * time.Millisecond
	retryMaxDelay         = 800 * time.Millisecond

	// defaultBreakerThreshold 连续失败多少次后打开熔断。
	// 阈值按「同一进程内的工具名」计，不与全局请求量挂钩：
	// 一个检索后端已经连续失败 5 次，再让模型继续调用只会放大故障。
	defaultBreakerThreshold = 5
	// defaultBreakerCooldown 熔断打开后多久允许一次半开探测。
	defaultBreakerCooldown = 30 * time.Second
)

// ErrToolCircuitOpen 熔断器打开后直接拒绝调用，调用方可用 errors.Is 判断。
var ErrToolCircuitOpen = errors.New("工具熔断器已打开")

// ToolPolicy 描述单个工具的执行策略。
type ToolPolicy struct {
	// ReadOnly 表示工具没有副作用，可安全做同参合并、重试和熔断。
	// 它与 Cacheable 分开：图谱检索结果变化快，适合去重/重试但未必适合缓存。
	ReadOnly bool
	// Cacheable 只读工具可开启；有副作用的工具开启会导致重复操作被吞。
	// 开启后同时具备 ReadOnly 语义。
	Cacheable bool
	// Retryable 表示失败可安全重试，必须只对幂等工具开启。
	// 它与 Cacheable 分开：只读但结果变化快的工具可以缓存，却未必适合重试；
	// 有副作用的工具两个都不应开启。
	Retryable bool
	// Breaker 为只读工具开启熔断；有副作用的工具不应开启。
	Breaker bool
	// MaxAttempts 为 0 时，Retryable 工具使用 defaultRetryAttempts。
	MaxAttempts int
	// Timeout 为 0 时使用 DefaultToolTimeout。
	Timeout time.Duration
}

// readOnly 判断是否走只读工具路径（同参合并、可选缓存/重试/熔断）。
func (p ToolPolicy) readOnly() bool { return p.ReadOnly || p.Cacheable }

type toolCacheEntry struct {
	result  string
	expires time.Time
}

type inflightCall struct {
	done   chan struct{}
	result string
	err    error
}

type breakerState int

const (
	breakerClosed breakerState = iota
	breakerOpen
	breakerHalfOpen
)

type toolBreaker struct {
	state    breakerState
	failures int
	openedAt time.Time
	probing  bool
}

// permanentToolError 标记「重试没有意义」的错误：参数错误、panic、熔断拒绝。
// 用类型而不是字符串判断，避免业务错误文案变化后重试策略悄悄失效。
type permanentToolError struct{ err error }

func (e *permanentToolError) Error() string { return e.err.Error() }
func (e *permanentToolError) Unwrap() error { return e.err }

// isPermanentToolError 判断一次失败是否值得重试。
//
// 内部包装（panic、熔断拒绝）与工具层显式哨兵错误共用同一个判定；
// 未知错误仍然保留「可重试」的旧行为，避免第三方工具因为还没接入
// 哨兵错误就突然失去重试能力。
func isPermanentToolError(err error) bool {
	var permanent *permanentToolError
	if errors.As(err, &permanent) {
		return true
	}
	switch {
	case errors.Is(err, toolerr.ErrToolNotFound),
		errors.Is(err, toolerr.ErrInvalidArgs),
		errors.Is(err, toolerr.ErrPermission),
		errors.Is(err, context.Canceled):
		return true
	default:
		return false
	}
}

// Invoker 统一的工具执行器：超时、panic 隔离、并发上限、同参去重、只读缓存。
type Invoker struct {
	timeout time.Duration
	sem     chan struct{}

	mu       sync.Mutex
	inflight map[string]*inflightCall
	cache    map[string]toolCacheEntry
	breakers map[string]*toolBreaker
	now      func() time.Time

	retryBase       time.Duration
	breakerLimit    int
	breakerCooldown time.Duration
}

// NewInvoker 创建执行器；timeout<=0 用默认值。
func NewInvoker(timeout time.Duration) *Invoker {
	if timeout <= 0 {
		timeout = DefaultToolTimeout
	}
	return &Invoker{
		timeout:         timeout,
		sem:             make(chan struct{}, DefaultToolConcurrency),
		inflight:        make(map[string]*inflightCall),
		cache:           make(map[string]toolCacheEntry),
		breakers:        make(map[string]*toolBreaker),
		now:             time.Now,
		retryBase:       defaultRetryBaseDelay,
		breakerLimit:    defaultBreakerThreshold,
		breakerCooldown: defaultBreakerCooldown,
	}
}

// Wrap 把任意 InvokableTool 包装成带策略的 BaseTool。
// Info 原样透传，工具名与 schema 不受影响。
func (i *Invoker) Wrap(raw tool.InvokableTool, policy ToolPolicy) tool.BaseTool {
	return &resilientTool{invoker: i, raw: raw, policy: policy}
}

type resilientTool struct {
	invoker *Invoker
	raw     tool.InvokableTool
	policy  ToolPolicy
}

func (t *resilientTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return t.raw.Info(ctx)
}

func (t *resilientTool) InvokableRun(ctx context.Context, arguments string, opts ...tool.Option) (string, error) {
	return t.invoker.invoke(ctx, t.raw, arguments, t.policy)
}

func (i *Invoker) invoke(ctx context.Context, raw tool.InvokableTool, arguments string, policy ToolPolicy) (string, error) {
	name := "unknown"
	if info, err := raw.Info(ctx); err == nil && info != nil {
		name = info.Name
	}
	key := name + "\x00" + arguments

	var (
		result string
		err    error
	)

	switch {
	case !policy.readOnly():
		// 有副作用的工具不做任何合并或缓存：
		// 两次相同参数的「发消息」是两次真实操作，合并等于吞掉一次。
		// 同时也不做重试和熔断：业务失败（如余额不足、无权限）不应被当成
		// 后端故障，更不该让一次失败挡住后续所有发送。
		result, err = i.execute(ctx, name, raw, arguments, policy, &inflightCall{}, false)
	default:
		// 1) 只读缓存：同一参数在短时间内重复查询直接复用。
		//    放在 inflight 之前，避免「缓存已命中还要抢并发额度」。
		if policy.Cacheable {
			if cached, ok := i.lookupCache(key); ok {
				result = cached
				break
			}
		}

		// 2) 同参合并：模型在同一轮里重复调用同一工具是常见行为
		//    （并行分支、重试），并发请求没有理由打两次外部服务。
		//    先加入 inflight 再检查熔断：已有调用在跑时，等待者只是共享结果，
		//    不应该被熔断器二次拒绝。
		call, shared, joinErr := i.joinInflight(ctx, key)
		if joinErr != nil {
			err = joinErr
			break
		}
		if shared {
			result, err = call.result, call.err
			break
		}
		defer i.finishInflight(key, call)

		allowed, halfOpen := true, false
		if policy.Breaker && policy.readOnly() {
			allowed, halfOpen = i.allow(ctx, name)
		}
		if !allowed {
			err = &permanentToolError{err: fmt.Errorf("%w: %s（连续失败，请稍后重试）", ErrToolCircuitOpen, name)}
			call.err = err
			obs.RecordToolCall(ctx, name, "rejected")
			break
		}
		result, err = i.execute(ctx, name, raw, arguments, policy, call, halfOpen)
	}

	if err != nil {
		return "", err
	}
	// 3) 单轮总预算：缓存命中与同参共享同样要扣额度——它们的内容一样会进 prompt，
	//    不扣就会出现「同一轮反复命中缓存却没有上限」的漏洞。
	return i.applyTurnBudget(ctx, name, result), nil
}

// execute 执行一次逻辑调用，并在策略允许时重试。
//
// 重试只发生在「同一逻辑调用」内部：inflight 的其他等待者看到的仍是最终结果，
// 不会因为第一次失败就各自再打一次后端。
func (i *Invoker) execute(
	ctx context.Context,
	name string,
	raw tool.InvokableTool,
	arguments string,
	policy ToolPolicy,
	call *inflightCall,
	halfOpen bool,
) (string, error) {
	// 重试和熔断都只对只读工具生效。即使调用方误把 Retryable 配到有副作用的
	// 工具上，这里也不会重复执行「发消息」这类操作。
	retryable := policy.Retryable && policy.readOnly()
	breakerEnabled := policy.Breaker && policy.readOnly()

	attempts := 1
	if retryable {
		attempts = policy.MaxAttempts
		if attempts <= 0 {
			attempts = defaultRetryAttempts
		}
	}
	// 半开状态只允许一次探测；探测失败立即重新打开，不在半开里继续重试。
	if halfOpen {
		attempts = 1
	}

	// 总超时覆盖「所有重试」，而不是每次重试各拿一份超时。
	// 否则一次 20s 的后端挂起会在三次尝试后拖成 60s，反而比不重试更糟。
	totalTimeout := policy.Timeout
	if totalTimeout <= 0 {
		totalTimeout = i.timeout
	}
	callCtx, cancel := context.WithTimeout(ctx, totalTimeout)
	defer cancel()

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		result, err := i.runAttempt(callCtx, name, raw, arguments, policy)
		obs.RecordToolCall(ctx, name, classifyToolResult(err))

		if err == nil {
			i.recordSuccess(ctx, name, breakerEnabled)
			call.result = result
			if policy.Cacheable && result != "" {
				i.storeCache(keyOf(name, arguments), result)
			}
			return result, nil
		}

		lastErr = err
		call.err = err

		// 调用方主动取消（用户关页面、请求超时）不是后端故障：
		// 既不能重试，也不能计入熔断，否则一次批量取消就能把工具打成不可用。
		if ctx.Err() != nil {
			i.releaseHalfOpen(ctx, name, breakerEnabled, halfOpen)
			return "", err
		}
		if callCtx.Err() != nil {
			i.recordFailure(ctx, name, breakerEnabled)
			return "", err
		}

		if isPermanentToolError(err) || !retryable || attempt >= attempts {
			i.recordFailure(ctx, name, breakerEnabled)
			return "", err
		}

		obs.RecordToolRetry(ctx, name)
		logger.Warn("工具调用失败，准备重试",
			logger.StringField("tool", name),
			logger.IntField("attempt", attempt),
			logger.IntField("max_attempts", attempts),
			logger.ErrorField(err))

		delay := i.retryDelay(attempt)
		select {
		case <-time.After(delay):
		case <-callCtx.Done():
			i.releaseHalfOpen(ctx, name, breakerEnabled, halfOpen)
			return "", callCtx.Err()
		}
	}

	i.recordFailure(ctx, name, breakerEnabled)
	return "", lastErr
}

func classifyToolResult(err error) string {
	if err == nil {
		return "ok"
	}
	switch {
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, toolerr.ErrRateLimited):
		return "rate_limited"
	case errors.Is(err, toolerr.ErrTransient):
		return "transient"
	case errors.Is(err, toolerr.ErrToolNotFound):
		return "not_found"
	case errors.Is(err, toolerr.ErrInvalidArgs):
		return "invalid_args"
	case errors.Is(err, toolerr.ErrPermission):
		return "permission"
	}
	var permanent *permanentToolError
	if errors.As(err, &permanent) {
		return "rejected"
	}
	return "error"
}

func (i *Invoker) retryDelay(attempt int) time.Duration {
	base := i.retryBase
	if base <= 0 {
		base = defaultRetryBaseDelay
	}
	d := base
	for n := 1; n < attempt && d < retryMaxDelay; n++ {
		d *= 2
	}
	if d > retryMaxDelay {
		return retryMaxDelay
	}
	return d
}

// applyTurnBudget 按本轮共享预算裁剪工具结果。
//
// 与 result_truncate.go 的单条硬上限是两道独立闸门：
//   - 硬上限保证「一条结果不会吃光窗口」；
//   - 总预算保证「一条轮次里 N 个工具加起来也不会吃光窗口」。
//
// ctx 上没有挂预算（非 Agent 路径，如单测、脚本直接调用工具）时原样返回，
// 保持旧行为，不因为新机制影响已有调用方。
func (i *Invoker) applyTurnBudget(ctx context.Context, name, result string) string {
	turn := toolbudget.FromContext(ctx)
	if turn == nil || result == "" {
		return result
	}

	n := utf8.RuneCountInString(result)
	granted := turn.Take(n)
	if granted >= n {
		return result
	}

	// 额度已用尽：给模型一条明确说明，而不是返回空字符串。
	// 空结果会被模型理解成「工具坏了」并原地重试，而重试同样拿不到额度，
	// 只会把轮次耗在无意义的调用上。
	if granted <= 0 {
		logger.Warn("单轮工具结果预算已用尽",
			logger.StringField("tool", name),
			logger.IntField("runes", n))
		return fmt.Sprintf("（本轮工具结果预算已用尽，%s 返回的约 %d 字符未注入上下文；"+
			"如确需该内容，请在下一轮重新调用该工具。）", name, n)
	}

	logger.Warn("工具结果受单轮总预算限制，已截断",
		logger.StringField("tool", name),
		logger.IntField("runes", n),
		logger.IntField("granted", granted))
	return truncateMiddleOfToolResult(result, granted)
}

// runAttempt 执行单次工具调用。重试由上层 execute 负责，
// 这里只负责并发额度、单次超时、panic 隔离和结果硬上限。
//
// 返回值必须具名：panic 恢复后只能通过命名返回值把错误交给调用方，
// 否则 recover 里写入错误、外层却返回空字符串与 nil。
func (i *Invoker) runAttempt(ctx context.Context, name string, raw tool.InvokableTool, arguments string, policy ToolPolicy) (result string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = &permanentToolError{err: fmt.Errorf("工具 %s panic: %v", name, r)}
			logger.Error("工具执行 panic",
				logger.StringField("tool", name),
				logger.StringField("panic", fmt.Sprint(r)))
		}
	}()

	if err := ctx.Err(); err != nil {
		return "", err
	}

	// 3) 并发上限：拿不到额度时宁可排队等上下文结束，也不无限开 goroutine。
	select {
	case i.sem <- struct{}{}:
		defer func() { <-i.sem }()
	case <-ctx.Done():
		return "", ctx.Err()
	}

	timeout := policy.Timeout
	if timeout <= 0 {
		timeout = i.timeout
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	result, err = raw.InvokableRun(callCtx, arguments)
	if err != nil {
		return "", err
	}

	// 4) 结果硬上限：各工具自己的预算可能漏掉，这里是最后一道闸。
	//    截断保留头尾而不是只留头部：工具结果常在末尾给结论/汇总，
	//    砍掉尾部等于把最有用的部分扔掉。
	if n := utf8.RuneCountInString(result); n > toolResultRuneLimit {
		result = truncateMiddleOfToolResult(result, toolResultRuneLimit)
		logger.Warn("工具结果超过硬上限，已截断",
			logger.StringField("tool", name),
			logger.IntField("runes", n))
	}

	return result, nil
}

// allow 判断当前是否允许调用，并返回这是否是一次半开探测。
//
// 半开状态只放行一个探测请求；探测成功才恢复，失败立即重新打开。
// 这样后端刚恢复时不会被瞬间涌入的并发再次打垮。
func (i *Invoker) allow(ctx context.Context, name string) (allowed, halfOpen bool) {
	i.mu.Lock()
	breaker := i.breakers[name]
	if breaker == nil {
		breaker = &toolBreaker{}
		i.breakers[name] = breaker
	}

	event := ""
	switch breaker.state {
	case breakerOpen:
		cooldown := i.breakerCooldown
		if cooldown <= 0 {
			cooldown = defaultBreakerCooldown
		}
		if i.now().Sub(breaker.openedAt) >= cooldown {
			breaker.state = breakerHalfOpen
			breaker.probing = true
			allowed = true
			halfOpen = true
			event = "half_open"
		} else {
			event = "open_rejected"
		}
	case breakerHalfOpen:
		if breaker.probing {
			event = "half_open_rejected"
		} else {
			breaker.probing = true
			allowed = true
			halfOpen = true
			event = "half_open"
		}
	default:
		allowed = true
	}
	i.mu.Unlock()

	if event != "" {
		obs.RecordToolBreaker(ctx, name, event)
	}
	return allowed, halfOpen
}

func (i *Invoker) recordSuccess(ctx context.Context, name string, enabled bool) {
	if !enabled {
		return
	}

	i.mu.Lock()
	breaker := i.breakers[name]
	if breaker == nil {
		i.mu.Unlock()
		return
	}
	wasClosed := breaker.state == breakerClosed
	breaker.state = breakerClosed
	breaker.failures = 0
	breaker.probing = false
	i.mu.Unlock()

	if !wasClosed {
		obs.RecordToolBreaker(ctx, name, "closed")
	}
}

func (i *Invoker) recordFailure(ctx context.Context, name string, enabled bool) {
	if !enabled {
		return
	}

	event := ""
	i.mu.Lock()
	breaker := i.breakers[name]
	if breaker == nil {
		breaker = &toolBreaker{}
		i.breakers[name] = breaker
	}

	switch breaker.state {
	case breakerHalfOpen:
		breaker.state = breakerOpen
		breaker.openedAt = i.now()
		breaker.probing = false
		event = "open"
	case breakerClosed:
		breaker.failures++
		limit := i.breakerLimit
		if limit <= 0 {
			limit = defaultBreakerThreshold
		}
		if breaker.failures >= limit {
			breaker.state = breakerOpen
			breaker.openedAt = i.now()
			event = "open"
		}
	}
	i.mu.Unlock()

	if event != "" {
		obs.RecordToolBreaker(ctx, name, event)
		logger.Warn("工具连续失败，熔断器已打开",
			logger.StringField("tool", name),
			logger.IntField("threshold", i.breakerLimit))
	}
}

// releaseHalfOpen 处理「调用方取消」这类中性结果：
// 探测请求没有给出后端是否恢复的结论，因此回到 open 而不是误判成功/失败。
func (i *Invoker) releaseHalfOpen(_ context.Context, name string, enabled, halfOpen bool) {
	if !enabled || !halfOpen {
		return
	}

	i.mu.Lock()
	if breaker := i.breakers[name]; breaker != nil && breaker.state == breakerHalfOpen {
		breaker.state = breakerOpen
		breaker.openedAt = i.now()
		breaker.probing = false
	}
	i.mu.Unlock()
}

func keyOf(name, arguments string) string { return name + "\x00" + arguments }

// joinInflight 返回 (call, shared, err)。
// shared=false 表示由调用方独占执行；shared=true 表示已有同参调用在跑，
// 等待结束后读 call.result / call.err 即可。
//
// 等待者必须能被自己的 ctx 取消：发起者可能比当前请求慢得多，
// 不能因为「搭便车」就把已经断开的请求继续挂在别人的调用上。
func (i *Invoker) joinInflight(ctx context.Context, key string) (*inflightCall, bool, error) {
	i.mu.Lock()
	if call, ok := i.inflight[key]; ok {
		i.mu.Unlock()
		select {
		case <-call.done:
			return call, true, nil
		case <-ctx.Done():
			return nil, true, ctx.Err()
		}
	}
	call := &inflightCall{done: make(chan struct{})}
	i.inflight[key] = call
	i.mu.Unlock()
	return call, false, nil
}

func (i *Invoker) finishInflight(key string, call *inflightCall) {
	i.mu.Lock()
	delete(i.inflight, key)
	i.mu.Unlock()
	close(call.done)
}

func (i *Invoker) lookupCache(key string) (string, bool) {
	i.mu.Lock()
	defer i.mu.Unlock()

	entry, ok := i.cache[key]
	if !ok || i.now().After(entry.expires) {
		if ok {
			delete(i.cache, key)
		}
		return "", false
	}
	return entry.result, true
}

func (i *Invoker) storeCache(key, result string) {
	i.mu.Lock()
	defer i.mu.Unlock()

	if len(i.cache) >= toolCacheMaxEntries {
		// 简单的整表清理：工具结果缓存的价值只在「同轮重复查询」，
		// 超出上限时全部丢弃比维护 LRU 更便宜。
		i.cache = make(map[string]toolCacheEntry)
	}
	i.cache[key] = toolCacheEntry{result: result, expires: i.now().Add(toolCacheTTL)}
}
