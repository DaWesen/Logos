package tools

import (
	"context"
	"strings"
	"testing"

	"Logos/internal/bot/toolbudget"

	"github.com/cloudwego/eino/components/tool"
)

func runTool(t *testing.T, ctx context.Context, bt tool.BaseTool, args string) string {
	t.Helper()
	it, ok := bt.(tool.InvokableTool)
	if !ok {
		t.Fatalf("工具 %T 不可调用", bt)
	}
	got, err := it.InvokableRun(ctx, args)
	if err != nil {
		t.Fatalf("工具调用失败: %v", err)
	}
	return got
}

// TestInvoker_TurnBudgetSharedAcrossTools 同一轮的不同工具共用一个预算：
// 第二个大结果只能拿到「剩余额度」，不能各自按 32000 硬上限通过。
func TestInvoker_TurnBudgetSharedAcrossTools(t *testing.T) {
	inv := NewInvoker(0)
	a := inv.Wrap(&stubTool{name: "kb1", out: strings.Repeat("a", 60)}, ToolPolicy{Cacheable: true})
	b := inv.Wrap(&stubTool{name: "kb2", out: strings.Repeat("b", 60)}, ToolPolicy{Cacheable: true})

	ctx, turn := toolbudget.WithTurn(context.Background(), 100)

	if got := runTool(t, ctx, a, `{"q":"a"}`); len([]rune(got)) != 60 {
		t.Fatalf("第一个结果应完整，got %d runes", len([]rune(got)))
	}
	got := runTool(t, ctx, b, `{"q":"b"}`)
	if n := len([]rune(got)); n != 40 {
		t.Fatalf("第二个结果应被裁到剩余 40 字符，got %d", n)
	}
	if !strings.Contains(got, toolResultTruncationMarker) {
		t.Fatalf("截断结果必须带省略标记，got %q", got)
	}
	if used := turn.Used(); used != 100 {
		t.Fatalf("已用额度 = %d, want 100", used)
	}
}

// TestInvoker_TurnBudgetExhaustedIsExplicit 预算用尽时返回明确说明而不是空字符串：
// 空结果会被模型当成工具故障并原地重试，而重试同样拿不到额度。
func TestInvoker_TurnBudgetExhaustedIsExplicit(t *testing.T) {
	inv := NewInvoker(0)
	first := inv.Wrap(&stubTool{name: "kb1", out: strings.Repeat("a", 10)}, ToolPolicy{})
	second := inv.Wrap(&stubTool{name: "kb2", out: strings.Repeat("b", 20)}, ToolPolicy{})

	ctx, turn := toolbudget.WithTurn(context.Background(), 10)

	runTool(t, ctx, first, `{}`)
	got := runTool(t, ctx, second, `{}`)

	if !strings.Contains(got, "预算已用尽") || !strings.Contains(got, "kb2") {
		t.Fatalf("额度耗尽必须返回可读说明，got %q", got)
	}
	if strings.Contains(got, "bbbb") {
		t.Fatalf("额度耗尽不应注入正文内容，got %q", got)
	}
	if rem := turn.Remaining(); rem != 0 {
		t.Fatalf("剩余 = %d, want 0", rem)
	}
}

// TestInvoker_TurnBudgetPerTurn 预算按轮隔离：上一轮用光不影响下一轮。
func TestInvoker_TurnBudgetPerTurn(t *testing.T) {
	inv := NewInvoker(0)
	wrapped := inv.Wrap(&stubTool{name: "kb", out: strings.Repeat("x", 50)}, ToolPolicy{Cacheable: true})

	for i := 0; i < 2; i++ {
		ctx, _ := toolbudget.WithTurn(context.Background(), 50)
		if got := runTool(t, ctx, wrapped, `{"q":"same"}`); len([]rune(got)) != 50 {
			t.Fatalf("第 %d 轮结果应完整，got %d", i+1, len([]rune(got)))
		}
	}
}

// TestInvoker_NoTurnBudgetUnchanged 没有挂预算的调用路径（脚本、单测直接调工具）
// 保持旧行为，只受单条硬上限约束。
func TestInvoker_NoTurnBudgetUnchanged(t *testing.T) {
	inv := NewInvoker(0)
	wrapped := inv.Wrap(&stubTool{name: "big", out: strings.Repeat("x", toolResultRuneLimit)}, ToolPolicy{})

	got := runTool(t, context.Background(), wrapped, `{}`)
	if n := len([]rune(got)); n != toolResultRuneLimit {
		t.Fatalf("无预算时结果长度 = %d, want %d", n, toolResultRuneLimit)
	}
}

// TestInvoker_TurnBudgetChargesCacheHit 缓存命中同样消耗预算：
// 内容一样会进 prompt，不扣就会出现「同参反复命中、上下文无上限」的漏洞。
func TestInvoker_TurnBudgetChargesCacheHit(t *testing.T) {
	inv := NewInvoker(0)
	stub := &stubTool{name: "kb", out: strings.Repeat("c", 8)}
	wrapped := inv.Wrap(stub, ToolPolicy{Cacheable: true})

	ctx, _ := toolbudget.WithTurn(context.Background(), 10)

	if got := runTool(t, ctx, wrapped, `{"q":"same"}`); len([]rune(got)) != 8 {
		t.Fatalf("首次结果应完整，got %d", len([]rune(got)))
	}
	// 第二次命中缓存，但只剩 2 字符额度
	if got := runTool(t, ctx, wrapped, `{"q":"same"}`); len([]rune(got)) != 2 {
		t.Fatalf("缓存命中应被剩余额度裁剪到 2，got %d", len([]rune(got)))
	}
	if n := stub.callCount(); n != 1 {
		t.Fatalf("缓存命中不应重复调用后端，执行 %d 次", n)
	}
}

// TestInvoker_TurnBudgetIndependentTools 预算不跨工具执行器共享：
// 执行器是进程级单例，只有 ctx 上的 Turn 才能区分「哪一轮」。
func TestInvoker_TurnBudgetIndependentTools(t *testing.T) {
	stub := &stubTool{name: "kb", out: strings.Repeat("y", 30)}

	invA := NewInvoker(0)
	invB := NewInvoker(0)
	a := invA.Wrap(stub, ToolPolicy{})
	b := invB.Wrap(stub, ToolPolicy{})

	ctxA, _ := toolbudget.WithTurn(context.Background(), 30)
	ctxB, _ := toolbudget.WithTurn(context.Background(), 30)

	if got := runTool(t, ctxA, a, `{"q":"a"}`); len([]rune(got)) != 30 {
		t.Fatalf("A 轮结果应完整，got %d", len([]rune(got)))
	}
	if got := runTool(t, ctxB, b, `{"q":"b"}`); len([]rune(got)) != 30 {
		t.Fatalf("B 轮不应该受 A 轮消耗影响，got %d", len([]rune(got)))
	}
}
