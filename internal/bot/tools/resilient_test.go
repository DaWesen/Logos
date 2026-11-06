package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"Logos/internal/bot/toolerr"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// stubTool 可控的 InvokableTool 桩：记录调用次数、可延时、可 panic。
type stubTool struct {
	name  string
	mu    sync.Mutex
	calls int
	delay time.Duration
	panic bool
	err   error
	errs  []error
	out   string
	block chan struct{} // 非 nil 时等待关闭后才返回，用于制造并发重叠
}

func (s *stubTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: s.name}, nil
}

func (s *stubTool) InvokableRun(ctx context.Context, _ string, _ ...tool.Option) (string, error) {
	s.mu.Lock()
	s.calls++
	callNo := s.calls
	s.mu.Unlock()

	if s.block != nil {
		select {
		case <-s.block:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	if s.delay > 0 {
		select {
		case <-time.After(s.delay):
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	if s.panic {
		panic("boom")
	}
	if len(s.errs) > 0 {
		idx := callNo - 1
		if idx >= len(s.errs) {
			idx = len(s.errs) - 1
		}
		if err := s.errs[idx]; err != nil {
			return "", err
		}
		return s.out, nil
	}
	if s.err != nil {
		return "", s.err
	}
	return s.out, nil
}

func (s *stubTool) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// TestInvoker_SameArgsDeduped 只读工具的同参并发调用只应打到后端一次，
// 其余调用共享同一份结果（模型重试/并行分支很常见）。
func TestInvoker_SameArgsDeduped(t *testing.T) {
	stub := &stubTool{name: "search", out: "result", block: make(chan struct{})}
	inv := NewInvoker(time.Second)
	wrapped := inv.Wrap(stub, ToolPolicy{Cacheable: true})

	var wg sync.WaitGroup
	for n := 0; n < 3; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := wrapped.(tool.InvokableTool).InvokableRun(context.Background(), `{"q":"x"}`)
			if err != nil || got != "result" {
				t.Errorf("got %q, err %v", got, err)
			}
		}()
	}

	// 等所有 goroutine 进入 inflight 后再放行
	time.Sleep(50 * time.Millisecond)
	close(stub.block)
	wg.Wait()

	if n := stub.callCount(); n != 1 {
		t.Fatalf("同参并发调用执行了 %d 次，want 1", n)
	}
}

// TestInvoker_SideEffectNotMerged 有副作用的工具即使同参并发也不能合并：
// 两次「发送消息」是两次真实操作，合并等于吞掉一次。
func TestInvoker_SideEffectNotMerged(t *testing.T) {
	stub := &stubTool{name: "send", out: "ok", block: make(chan struct{})}
	inv := NewInvoker(time.Second)
	wrapped := inv.Wrap(stub, ToolPolicy{})

	var wg sync.WaitGroup
	for n := 0; n < 2; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := wrapped.(tool.InvokableTool).InvokableRun(context.Background(), `{}`); err != nil {
				t.Error(err)
			}
		}()
	}

	deadline := time.Now().Add(time.Second)
	for stub.callCount() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if n := stub.callCount(); n != 2 {
		t.Fatalf("副作用工具不应合并，执行了 %d 次", n)
	}
	close(stub.block)
	wg.Wait()
}

// TestInvoker_CacheReuse 只读工具在 TTL 内重复调用不再打后端。
func TestInvoker_CacheReuse(t *testing.T) {
	stub := &stubTool{name: "kb", out: "cached"}
	inv := NewInvoker(time.Second)
	wrapped := inv.Wrap(stub, ToolPolicy{Cacheable: true})

	for n := 0; n < 3; n++ {
		got, err := wrapped.(tool.InvokableTool).InvokableRun(context.Background(), `{"q":"same"}`)
		if err != nil || got != "cached" {
			t.Fatalf("got %q, err %v", got, err)
		}
	}
	if n := stub.callCount(); n != 1 {
		t.Fatalf("缓存未生效，执行了 %d 次", n)
	}

	// 不同参数不共享缓存
	if _, err := wrapped.(tool.InvokableTool).InvokableRun(context.Background(), `{"q":"other"}`); err != nil {
		t.Fatal(err)
	}
	if n := stub.callCount(); n != 2 {
		t.Fatalf("不同参数应重新调用，执行了 %d 次", n)
	}
}

// TestInvoker_CacheExpires TTL 过后必须重新调用，避免记忆类查询永久返回旧结果。
func TestInvoker_CacheExpires(t *testing.T) {
	stub := &stubTool{name: "kb", out: "v1"}
	inv := NewInvoker(time.Second)
	now := time.Now()
	inv.now = func() time.Time { return now }
	wrapped := inv.Wrap(stub, ToolPolicy{Cacheable: true})

	if _, err := wrapped.(tool.InvokableTool).InvokableRun(context.Background(), `{}`); err != nil {
		t.Fatal(err)
	}
	now = now.Add(toolCacheTTL + time.Second)

	if _, err := wrapped.(tool.InvokableTool).InvokableRun(context.Background(), `{}`); err != nil {
		t.Fatal(err)
	}
	if n := stub.callCount(); n != 2 {
		t.Fatalf("TTL 过期后应重新调用，执行了 %d 次", n)
	}
}

// TestInvoker_PanicIsolated 工具 panic 必须转成 error，不能穿透到对话链路。
func TestInvoker_PanicIsolated(t *testing.T) {
	stub := &stubTool{name: "bad", panic: true}
	inv := NewInvoker(time.Second)
	wrapped := inv.Wrap(stub, ToolPolicy{})

	_, err := wrapped.(tool.InvokableTool).InvokableRun(context.Background(), `{}`)
	if err == nil || !strings.Contains(err.Error(), "panic") {
		t.Fatalf("want panic error, got %v", err)
	}
}

// TestInvoker_Timeout 工具超时必须返回 deadline 错误，而不是一直挂着。
func TestInvoker_Timeout(t *testing.T) {
	stub := &stubTool{name: "slow", block: make(chan struct{})}
	defer close(stub.block)

	inv := NewInvoker(time.Second)
	wrapped := inv.Wrap(stub, ToolPolicy{Timeout: 50 * time.Millisecond})

	_, err := wrapped.(tool.InvokableTool).InvokableRun(context.Background(), `{}`)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want DeadlineExceeded, got %v", err)
	}
}

// TestInvoker_ParentCancelPropagates 上层取消（用户中断）要立刻传导给工具。
func TestInvoker_ParentCancelPropagates(t *testing.T) {
	stub := &stubTool{name: "slow", block: make(chan struct{})}
	defer close(stub.block)

	ctx, cancel := context.WithCancel(context.Background())
	inv := NewInvoker(time.Second)
	wrapped := inv.Wrap(stub, ToolPolicy{})

	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	_, err := wrapped.(tool.InvokableTool).InvokableRun(ctx, `{}`)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want Canceled, got %v", err)
	}
}

// TestInvoker_ResultHardLimit 结果超过硬上限时截断，防止新工具漏做预算。
func TestInvoker_ResultHardLimit(t *testing.T) {
	stub := &stubTool{name: "big", out: strings.Repeat("x", toolResultRuneLimit+100)}
	inv := NewInvoker(time.Second)
	wrapped := inv.Wrap(stub, ToolPolicy{})

	got, err := wrapped.(tool.InvokableTool).InvokableRun(context.Background(), `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if len([]rune(got)) != toolResultRuneLimit {
		t.Fatalf("结果长度 = %d, want %d", len([]rune(got)), toolResultRuneLimit)
	}
}

// TestInvoker_ConcurrencyLimit 并发额度用尽时，调用应排队而不是继续开 goroutine。
func TestInvoker_ConcurrencyLimit(t *testing.T) {
	inv := NewInvoker(time.Second)
	inv.sem = make(chan struct{}, 1)

	stub := &stubTool{name: "slow", out: "ok", block: make(chan struct{})}
	wrapped := inv.Wrap(stub, ToolPolicy{})

	var started int32
	var wg sync.WaitGroup
	for n := 0; n < 2; n++ {
		wg.Add(1)
		// 参数必须不同，否则会先被「同参合并」命中，测不到并发额度
		args := `{"n":` + string(rune('0'+n)) + `}`
		go func() {
			defer wg.Done()
			atomic.AddInt32(&started, 1)
			_, _ = wrapped.(tool.InvokableTool).InvokableRun(context.Background(), args)
		}()
	}

	time.Sleep(30 * time.Millisecond)
	if got := atomic.LoadInt32(&started); got != 2 {
		t.Fatalf("两个调用都应已发起，got %d", got)
	}
	if n := stub.callCount(); n != 1 {
		t.Fatalf("第二个调用应被并发额度阻塞，实际执行 %d 次", n)
	}
	close(stub.block)
	wg.Wait()
}

// TestInvoker_ErrorsNotCached 失败结果不能进缓存，否则一次抖动会被复用 TTL 时长。
func TestInvoker_ErrorsNotCached(t *testing.T) {
	stub := &stubTool{name: "kb", out: "ok", err: errors.New("down")}
	inv := NewInvoker(time.Second)
	wrapped := inv.Wrap(stub, ToolPolicy{Cacheable: true})

	for n := 0; n < 2; n++ {
		if _, err := wrapped.(tool.InvokableTool).InvokableRun(context.Background(), `{}`); err == nil {
			t.Fatal("期望错误")
		}
	}
	if n := stub.callCount(); n != 2 {
		t.Fatalf("错误结果不应缓存，执行了 %d 次", n)
	}
}

// TestInvoker_RetriesReadOnlyTool 只读工具遇到瞬时错误应重试，最终成功。
func TestInvoker_RetriesReadOnlyTool(t *testing.T) {
	transient := errors.New("temporary")
	stub := &stubTool{
		name: "kb",
		out:  "recovered",
		errs: []error{transient, transient, nil},
	}
	inv := NewInvoker(time.Second)
	inv.retryBase = time.Millisecond
	wrapped := inv.Wrap(stub, ToolPolicy{ReadOnly: true, Retryable: true})

	got, err := wrapped.(tool.InvokableTool).InvokableRun(context.Background(), `{"q":"x"}`)
	if err != nil {
		t.Fatalf("重试后应成功: %v", err)
	}
	if got != "recovered" {
		t.Fatalf("got %q, want recovered", got)
	}
	if n := stub.callCount(); n != 3 {
		t.Fatalf("应执行 3 次（首次 + 2 次重试），got %d", n)
	}
}

// TestInvoker_SideEffectNeverRetried 即使误把 Retryable 传到有副作用的工具上，
// 执行器也必须拒绝重试，避免「发消息」被执行两次。
func TestInvoker_SideEffectNeverRetried(t *testing.T) {
	stub := &stubTool{
		name: "send",
		out:  "ok",
		errs: []error{errors.New("temporary"), nil},
	}
	inv := NewInvoker(time.Second)
	inv.retryBase = time.Millisecond
	wrapped := inv.Wrap(stub, ToolPolicy{Retryable: true})

	if _, err := wrapped.(tool.InvokableTool).InvokableRun(context.Background(), `{}`); err == nil {
		t.Fatal("期望首次失败")
	}
	if n := stub.callCount(); n != 1 {
		t.Fatalf("有副作用的工具不能重试，执行了 %d 次", n)
	}
}

// TestInvoker_PanicNotRetried panic 是确定性 bug，重试只会放大日志和副作用风险。
func TestInvoker_PanicNotRetried(t *testing.T) {
	stub := &stubTool{name: "bad", panic: true}
	inv := NewInvoker(time.Second)
	inv.retryBase = time.Millisecond
	wrapped := inv.Wrap(stub, ToolPolicy{ReadOnly: true, Retryable: true})

	if _, err := wrapped.(tool.InvokableTool).InvokableRun(context.Background(), `{}`); err == nil {
		t.Fatal("期望 panic 错误")
	}
	if n := stub.callCount(); n != 1 {
		t.Fatalf("panic 不应重试，执行了 %d 次", n)
	}
}

// TestInvoker_BreakerOpensAndRejects 连续失败达到阈值后，后续调用应快速失败，
// 不再打后端。
func TestInvoker_BreakerOpensAndRejects(t *testing.T) {
	stub := &stubTool{name: "kb", err: errors.New("down")}
	inv := NewInvoker(time.Second)
	inv.breakerLimit = 2
	inv.breakerCooldown = time.Minute
	wrapped := inv.Wrap(stub, ToolPolicy{ReadOnly: true, Breaker: true})

	for i := 0; i < 2; i++ {
		if _, err := wrapped.(tool.InvokableTool).InvokableRun(context.Background(), fmt.Sprintf(`{"q":%d}`, i)); err == nil {
			t.Fatal("期望后端失败")
		}
	}

	_, err := wrapped.(tool.InvokableTool).InvokableRun(context.Background(), `{"q":3}`)
	if !errors.Is(err, ErrToolCircuitOpen) {
		t.Fatalf("熔断后应返回 ErrToolCircuitOpen, got %v", err)
	}
	if n := stub.callCount(); n != 2 {
		t.Fatalf("熔断打开后不应再调用后端，执行了 %d 次", n)
	}
}

// TestInvoker_BreakerHalfOpenRecovers 冷却后只放一次探测；成功则恢复关闭状态。
func TestInvoker_BreakerHalfOpenRecovers(t *testing.T) {
	stub := &stubTool{name: "kb", err: errors.New("down")}
	inv := NewInvoker(time.Second)
	now := time.Now()
	inv.now = func() time.Time { return now }
	inv.breakerLimit = 2
	inv.breakerCooldown = time.Second
	wrapped := inv.Wrap(stub, ToolPolicy{ReadOnly: true, Breaker: true})

	for i := 0; i < 2; i++ {
		_, _ = wrapped.(tool.InvokableTool).InvokableRun(context.Background(), fmt.Sprintf(`{"q":%d}`, i))
	}

	now = now.Add(2 * time.Second)
	stub.err = nil
	stub.out = "back"
	got, err := wrapped.(tool.InvokableTool).InvokableRun(context.Background(), `{"q":"probe"}`)
	if err != nil || got != "back" {
		t.Fatalf("半开探测应成功，got %q, err %v", got, err)
	}

	// 恢复后再调用一次，确认状态已闭合并继续正常服务。
	if got, err := wrapped.(tool.InvokableTool).InvokableRun(context.Background(), `{"q":"after"}`); err != nil || got != "back" {
		t.Fatalf("熔断恢复后应正常工作，got %q, err %v", got, err)
	}
	if n := stub.callCount(); n != 4 {
		t.Fatalf("期望 4 次后端调用，got %d", n)
	}
}

// TestInvoker_BreakerHalfOpenFailureReopens 半开探测失败必须立即重新打开。
func TestInvoker_BreakerHalfOpenFailureReopens(t *testing.T) {
	stub := &stubTool{name: "kb", err: errors.New("down")}
	inv := NewInvoker(time.Second)
	now := time.Now()
	inv.now = func() time.Time { return now }
	inv.breakerLimit = 2
	inv.breakerCooldown = time.Second
	wrapped := inv.Wrap(stub, ToolPolicy{ReadOnly: true, Breaker: true})

	for i := 0; i < 2; i++ {
		_, _ = wrapped.(tool.InvokableTool).InvokableRun(context.Background(), fmt.Sprintf(`{"q":%d}`, i))
	}
	now = now.Add(2 * time.Second)

	if _, err := wrapped.(tool.InvokableTool).InvokableRun(context.Background(), `{"q":"probe"}`); err == nil {
		t.Fatal("半开探测期望失败")
	}
	_, err := wrapped.(tool.InvokableTool).InvokableRun(context.Background(), `{"q":"after"}`)
	if !errors.Is(err, ErrToolCircuitOpen) {
		t.Fatalf("探测失败后应重新熔断, got %v", err)
	}
	if n := stub.callCount(); n != 3 {
		t.Fatalf("重新熔断后不应再调用后端，执行了 %d 次", n)
	}
}

// TestInvoker_CallerCancelDoesNotTripBreaker 调用方取消不是后端故障，
// 不能把工具打成熔断状态。
func TestInvoker_CallerCancelDoesNotTripBreaker(t *testing.T) {
	stub := &stubTool{name: "kb", out: "ok"}
	inv := NewInvoker(time.Second)
	inv.breakerLimit = 1
	wrapped := inv.Wrap(stub, ToolPolicy{ReadOnly: true, Breaker: true})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := wrapped.(tool.InvokableTool).InvokableRun(ctx, `{}`); !errors.Is(err, context.Canceled) {
		t.Fatalf("期望 context.Canceled, got %v", err)
	}

	got, err := wrapped.(tool.InvokableTool).InvokableRun(context.Background(), `{}`)
	if err != nil || got != "ok" {
		t.Fatalf("取消不应触发熔断，got %q, err %v", got, err)
	}
}

// TestInvoker_CacheHitSurvivesBreakerOpen 缓存命中不依赖后端，
// 即使熔断器已打开也应继续服务。
func TestInvoker_CacheHitSurvivesBreakerOpen(t *testing.T) {
	stub := &stubTool{name: "kb", out: "cached"}
	inv := NewInvoker(time.Second)
	inv.breakerLimit = 1
	wrapped := inv.Wrap(stub, ToolPolicy{Cacheable: true, Breaker: true})

	if _, err := wrapped.(tool.InvokableTool).InvokableRun(context.Background(), `{"q":"cached"}`); err != nil {
		t.Fatal(err)
	}

	stub.err = errors.New("down")
	_, _ = wrapped.(tool.InvokableTool).InvokableRun(context.Background(), `{"q":"other"}`)
	if _, err := wrapped.(tool.InvokableTool).InvokableRun(context.Background(), `{"q":"miss"}`); !errors.Is(err, ErrToolCircuitOpen) {
		t.Fatalf("新参数应被熔断拒绝, got %v", err)
	}

	got, err := wrapped.(tool.InvokableTool).InvokableRun(context.Background(), `{"q":"cached"}`)
	if err != nil || got != "cached" {
		t.Fatalf("缓存命中不应受熔断影响，got %q, err %v", got, err)
	}
}

// TestInvoker_SharedWaiterCanCancel 同参合并的等待者必须能被自己的 ctx 取消，
// 否则慢调用会把已经断开的请求继续挂在 goroutine 上。
func TestInvoker_SharedWaiterCanCancel(t *testing.T) {
	stub := &stubTool{name: "kb", out: "ok", block: make(chan struct{})}
	inv := NewInvoker(time.Second)
	wrapped := inv.Wrap(stub, ToolPolicy{ReadOnly: true})

	leaderDone := make(chan error, 1)
	go func() {
		_, err := wrapped.(tool.InvokableTool).InvokableRun(context.Background(), `{"q":"same"}`)
		leaderDone <- err
	}()

	deadline := time.Now().Add(time.Second)
	for stub.callCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if stub.callCount() == 0 {
		t.Fatal("leader 未进入工具调用")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := wrapped.(tool.InvokableTool).InvokableRun(ctx, `{"q":"same"}`)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("等待者应随自己的 ctx 超时, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("等待者被 leader 拖住了 %s", elapsed)
	}

	close(stub.block)
	if err := <-leaderDone; err != nil {
		t.Fatalf("leader 应成功: %v", err)
	}
}

// TestInvoker_RetrySharesTotalTimeout 重试共享一份总预算，
// 不能出现「每次重试各拿 20s，最终拖成 60s」。
func TestInvoker_RetrySharesTotalTimeout(t *testing.T) {
	stub := &stubTool{name: "slow", block: make(chan struct{})}
	inv := NewInvoker(time.Second)
	inv.retryBase = time.Millisecond
	wrapped := inv.Wrap(stub, ToolPolicy{
		ReadOnly:  true,
		Retryable: true,
		Timeout:   80 * time.Millisecond,
	})

	start := time.Now()
	_, err := wrapped.(tool.InvokableTool).InvokableRun(context.Background(), `{}`)
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("期望 DeadlineExceeded, got %v", err)
	}
	if elapsed > 400*time.Millisecond {
		t.Fatalf("总超时没有覆盖重试，耗时 %s", elapsed)
	}
	if n := stub.callCount(); n != 1 {
		t.Fatalf("总超时耗尽后不应继续重试，执行了 %d 次", n)
	}
}

// TestInvoker_PermanentSentinelsNotRetried 参数、权限、工具不存在这三类
// 失败重试多少次结果都一样，必须一次就返回，避免把确定性错误拖成慢请求。
func TestInvoker_PermanentSentinelsNotRetried(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{"tool_not_found", fmt.Errorf("lookup: %w", toolerr.ErrToolNotFound)},
		{"invalid_args", fmt.Errorf("decode: %w", toolerr.ErrInvalidArgs)},
		{"permission", fmt.Errorf("acl: %w", toolerr.ErrPermission)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stub := &stubTool{name: "kb", err: tt.err}
			inv := NewInvoker(time.Second)
			inv.retryBase = time.Millisecond
			wrapped := inv.Wrap(stub, ToolPolicy{ReadOnly: true, Retryable: true})

			if _, err := wrapped.(tool.InvokableTool).InvokableRun(context.Background(), `{}`); !errors.Is(err, tt.err) {
				t.Fatalf("want wrapped sentinel, got %v", err)
			}
			if n := stub.callCount(); n != 1 {
				t.Fatalf("永久失败不应重试，执行了 %d 次", n)
			}
		})
	}
}

// TestInvoker_RateLimitedIsRetried 限流是典型的瞬时失败：工具层用同一套
// 哨兵错误标记后，重试逻辑不需要认识具体供应商的错误码。
func TestInvoker_RateLimitedIsRetried(t *testing.T) {
	stub := &stubTool{
		name: "kb",
		out:  "recovered",
		errs: []error{toolerr.ErrRateLimited, nil},
	}
	inv := NewInvoker(time.Second)
	inv.retryBase = time.Millisecond
	wrapped := inv.Wrap(stub, ToolPolicy{ReadOnly: true, Retryable: true})

	got, err := wrapped.(tool.InvokableTool).InvokableRun(context.Background(), `{}`)
	if err != nil || got != "recovered" {
		t.Fatalf("got %q, err %v", got, err)
	}
	if n := stub.callCount(); n != 2 {
		t.Fatalf("限流应重试一次，执行了 %d 次", n)
	}
}
