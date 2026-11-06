package planner

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testPlan(t *testing.T, steps ...PlanStep) *Plan {
	t.Helper()
	for i := range steps {
		steps[i].Status = StatusPending
		if steps[i].Effect == "" {
			steps[i].Effect = EffectNone
		}
	}
	plan := &Plan{Goal: "测试目标", Steps: steps}
	if err := ValidatePlan(plan); err != nil {
		t.Fatalf("invalid test plan: %v", err)
	}
	return plan
}

func TestExecute_DAGOrderAndParallelism(t *testing.T) {
	plan := testPlan(t,
		PlanStep{ID: 1, Description: "读取文件"},
		PlanStep{ID: 2, Description: "读取目录"},
		PlanStep{ID: 3, Description: "汇总", DependsOn: []int{1, 2}},
	)
	var doneMu sync.Mutex
	done := map[int]bool{}
	var running, maxRunning atomic.Int32

	exec := func(ctx context.Context, step PlanStep, prior string) (StepOutcome, error) {
		cur := running.Add(1)
		for {
			old := maxRunning.Load()
			if cur <= old || maxRunning.CompareAndSwap(old, cur) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		running.Add(-1)
		if step.ID == 3 {
			doneMu.Lock()
			ok := done[1] && done[2]
			doneMu.Unlock()
			if !ok {
				return StepOutcome{}, fmt.Errorf("汇总步骤早于依赖完成")
			}
		}
		doneMu.Lock()
		done[step.ID] = true
		doneMu.Unlock()
		return StepOutcome{Output: fmt.Sprintf("out-%d", step.ID)}, nil
	}

	run, err := NewPlanner(nil).Execute(t.Context(), plan, exec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if run.Status != RunCompleted || !plan.AllDone() {
		t.Fatalf("status=%s err=%v", run.Status, run.Err)
	}
	if maxRunning.Load() < 2 {
		t.Errorf("独立步骤应并发执行，maxRunning=%d", maxRunning.Load())
	}
}

func TestExecute_FailureDegradesWithoutReplan(t *testing.T) {
	plan := testPlan(t,
		PlanStep{ID: 1, Description: "第一步"},
		PlanStep{ID: 2, Description: "第二步", DependsOn: []int{1}},
		PlanStep{ID: 3, Description: "第三步", DependsOn: []int{2}},
	)
	var executed atomic.Int32
	exec := func(ctx context.Context, step PlanStep, prior string) (StepOutcome, error) {
		executed.Add(1)
		return StepOutcome{}, ErrPermission
	}

	run, err := NewPlanner(nil).ExecuteWith(t.Context(), plan, exec, ExecuteOptions{
		Config: Config{DisableReplan: true},
	})
	if err != nil {
		t.Fatalf("degraded run should not return fatal error: %v", err)
	}
	if run.Status != RunDegraded {
		t.Fatalf("status=%s", run.Status)
	}
	if executed.Load() != 1 {
		t.Errorf("下游不应执行，executed=%d", executed.Load())
	}
	if plan.Steps[0].Status != StatusStale {
		t.Errorf("失败步骤应标记 stale，got %s", plan.Steps[0].Status)
	}
}

func TestExecute_ViolationPatchReusesUnaffectedResults(t *testing.T) {
	plan := testPlan(t,
		PlanStep{ID: 1, Description: "读取文件"},
		PlanStep{ID: 2, Description: "判断格式", DependsOn: []int{1}},
		PlanStep{ID: 3, Description: "解析文件", DependsOn: []int{2}},
		PlanStep{ID: 4, Description: "生成图表"},
		PlanStep{ID: 5, Description: "生成报告", DependsOn: []int{3, 4}},
	)
	patch := `{"reasoning":"格式判断错误","operations":[
		{"op":"replace_from","id":2,"steps":[
			{"local_id":"a","description":"按 XLSX 重新解析","depends_on":[],"effect":"none"},
			{"local_id":"b","description":"展开合并单元格","depends_on":["a"],"effect":"none"}]}]}`
	chat := &fakeChat{responses: []string{patch}}
	p := NewPlanner(chat.call)

	var mu sync.Mutex
	counts := map[int]int{}
	exec := func(ctx context.Context, step PlanStep, prior string) (StepOutcome, error) {
		mu.Lock()
		counts[step.ID]++
		mu.Unlock()
		out := StepOutcome{Output: fmt.Sprintf("out-%d", step.ID)}
		if step.ID == 3 {
			out.Violations = []Violation{{StepID: 2, Claim: "文件是 CSV", Reason: "实际是 XLSX"}}
		}
		return out, nil
	}

	run, err := p.Execute(t.Context(), plan, exec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if run.Status != RunCompleted || run.Replans != 1 {
		t.Fatalf("status=%s replans=%d err=%v", run.Status, run.Replans, run.Err)
	}
	if counts[1] != 1 || counts[4] != 1 {
		t.Errorf("未受影响步骤不应重跑，counts=%v", counts)
	}
	if counts[2] != 1 || counts[3] != 1 {
		t.Errorf("被替换步骤应只执行原计划一次，counts=%v", counts)
	}
	if plan.Step(2).Status != StatusSkipped {
		t.Errorf("被替换步骤应 skipped，got %s", plan.Step(2).Status)
	}
	if chat.calls != 1 {
		t.Errorf("应只重规划一次，calls=%d", chat.calls)
	}
	if !strings.Contains(chat.prompts[0], "失效集合：[2 3 5]") {
		t.Errorf("patch prompt 缺少失效集合：%s", chat.prompts[0])
	}
}

func TestExecute_StaleResultsNotInSynthesisPrompt(t *testing.T) {
	plan := testPlan(t,
		PlanStep{ID: 1, Description: "第一步"},
		PlanStep{ID: 2, Description: "第二步", DependsOn: []int{1}},
		PlanStep{ID: 3, Description: "第三步", DependsOn: []int{2}},
	)
	chat := &fakeChat{responses: []string{"not a patch"}}
	p := NewPlanner(chat.call)
	exec := func(ctx context.Context, step PlanStep, prior string) (StepOutcome, error) {
		out := StepOutcome{Output: "旧结论正文"}
		if step.ID == 3 {
			out.Violations = []Violation{{StepID: 1, Claim: "前提", Reason: "前提不成立"}}
		}
		return out, nil
	}

	run, err := p.Execute(t.Context(), plan, exec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if run.Status != RunDegraded {
		t.Fatalf("status=%s", run.Status)
	}
	text := FormatResults(run.Results)
	if strings.Contains(text, "旧结论正文") {
		t.Errorf("失效正文不应进入 prompt：%s", text)
	}
	if strings.Count(text, "【已失效】") != 3 {
		t.Errorf("失效步骤应全部带标记：%s", text)
	}
}

func TestExecute_BudgetExhausted(t *testing.T) {
	plan := testPlan(t,
		PlanStep{ID: 1, Description: "一"},
		PlanStep{ID: 2, Description: "二"},
	)
	exec := func(ctx context.Context, step PlanStep, prior string) (StepOutcome, error) {
		return StepOutcome{Output: "ok"}, nil
	}
	run, err := NewPlanner(nil).ExecuteWith(t.Context(), plan, exec, ExecuteOptions{
		Config: Config{MaxLLMCalls: 1, DisableReplan: true},
	})
	if err == nil {
		t.Fatal("budget exhaustion should return an error for the caller to degrade")
	}
	if run.Status != RunBudgetExhausted {
		t.Fatalf("status=%s", run.Status)
	}
	var done int
	for _, r := range run.Results {
		if r.Success {
			done++
		}
	}
	if done != 1 {
		t.Errorf("预算只允许 1 次调用，done=%d", done)
	}
}

func TestExecute_ExactBudgetStillCompletes(t *testing.T) {
	plan := testPlan(t,
		PlanStep{ID: 1, Description: "一"},
		PlanStep{ID: 2, Description: "二"},
	)
	exec := func(ctx context.Context, step PlanStep, prior string) (StepOutcome, error) {
		return StepOutcome{Output: "ok"}, nil
	}
	run, err := NewPlanner(nil).ExecuteWith(t.Context(), plan, exec, ExecuteOptions{
		Config: Config{MaxLLMCalls: 2, DisableReplan: true},
	})
	if err != nil || run.Status != RunCompleted {
		t.Fatalf("status=%v err=%v", run.Status, err)
	}
}

func TestExecute_TransientRetryDoesNotReplan(t *testing.T) {
	plan := testPlan(t, PlanStep{ID: 1, Description: "调用上游"})
	var calls atomic.Int32
	exec := func(ctx context.Context, step PlanStep, prior string) (StepOutcome, error) {
		if calls.Add(1) == 1 {
			return StepOutcome{}, ErrRateLimited
		}
		return StepOutcome{Output: "ok"}, nil
	}
	run, err := NewPlanner(nil).ExecuteWith(t.Context(), plan, exec, ExecuteOptions{
		Config: Config{MaxRetries: 1, RetryBase: time.Millisecond},
	})
	if err != nil || run.Status != RunCompleted {
		t.Fatalf("status=%v err=%v", run.Status, err)
	}
	if calls.Load() != 2 || run.Results[0].Attempt != 2 {
		t.Errorf("calls=%d attempt=%d", calls.Load(), run.Results[0].Attempt)
	}
	if run.Replans != 0 {
		t.Errorf("瞬时失败不应消耗重规划配额，replans=%d", run.Replans)
	}
}

func TestExecute_ProgressEventsAreOrdered(t *testing.T) {
	plan := testPlan(t,
		PlanStep{ID: 1, Description: "一"},
		PlanStep{ID: 2, Description: "二"},
	)
	var mu sync.Mutex
	var events []ProgressEvent
	exec := func(ctx context.Context, step PlanStep, prior string) (StepOutcome, error) {
		return StepOutcome{Output: "ok"}, nil
	}
	_, err := NewPlanner(nil).ExecuteWith(t.Context(), plan, exec, ExecuteOptions{
		Listener: func(ev ProgressEvent) {
			mu.Lock()
			events = append(events, ev)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) == 0 || events[0].Type != EventPlan {
		t.Fatalf("首个事件应为 plan，got %+v", events)
	}
	if events[len(events)-1].Type != EventDone {
		t.Fatalf("末个事件应为 done，got %+v", events[len(events)-1])
	}
	seenDone := map[int]bool{}
	for _, ev := range events {
		if ev.Type == EventStepDone {
			seenDone[ev.StepID] = true
		}
		if ev.Type == EventStepStart && seenDone[ev.StepID] {
			t.Errorf("step_start 出现在 step_done 之后: %+v", ev)
		}
	}
}

func TestStepTransition_StaleCannotRunDirectly(t *testing.T) {
	step := &PlanStep{ID: 1, Description: "x", Status: StatusDone}
	if err := step.Transition(StatusStale); err != nil {
		t.Fatalf("done -> stale should be legal: %v", err)
	}
	if err := step.Transition(StatusRunning); err == nil {
		t.Fatal("stale -> running must be rejected")
	}
	for _, to := range []StepStatus{StatusPending, StatusReady} {
		if err := step.Transition(to); err != nil {
			t.Fatalf("stale -> %s should be legal: %v", to, err)
		}
	}

	pending := &PlanStep{ID: 2, Status: StatusPending}
	if err := pending.Transition(StatusRunning); err == nil {
		t.Fatal("pending -> running must be rejected; schedule must go pending -> ready")
	}
}

func TestIdempotencyKey_StableAcrossAttempts(t *testing.T) {
	a := IdempotencyKey("run-1", 2, "send", []byte("hello"))
	b := IdempotencyKey("run-1", 2, "send", []byte("hello"))
	c := IdempotencyKey("run-2", 2, "send", []byte("hello"))
	if a != b || a == c || len(a) != 32 {
		t.Fatalf("a=%s b=%s c=%s", a, b, c)
	}
	ctx := WithIdempotencyKey(t.Context(), a)
	got, ok := IdempotencyKeyFrom(ctx)
	if !ok || got != a {
		t.Fatalf("got=%q ok=%v", got, ok)
	}
}

func TestEnsureLinearDependencies_OnlyWhenNoEdges(t *testing.T) {
	plan := testPlan(t,
		PlanStep{ID: 1, Description: "一"},
		PlanStep{ID: 2, Description: "二"},
		PlanStep{ID: 3, Description: "三"},
	)
	if !plan.EnsureLinearDependencies() {
		t.Fatal("无依赖计划应退化为线性")
	}
	for i := 1; i < len(plan.Steps); i++ {
		if got := plan.Steps[i].DependsOn; len(got) != 1 || got[0] != plan.Steps[i-1].ID {
			t.Errorf("step %d deps=%v", plan.Steps[i].ID, got)
		}
	}

	explicit := testPlan(t,
		PlanStep{ID: 1, Description: "一"},
		PlanStep{ID: 2, Description: "二", DependsOn: []int{1}},
	)
	if explicit.EnsureLinearDependencies() {
		t.Fatal("已有依赖时不应改写计划")
	}
}

func TestResults_CarryOutcomeAndDuration(t *testing.T) {
	plan := testPlan(t, PlanStep{ID: 1, Description: "一"})
	exec := func(ctx context.Context, step PlanStep, prior string) (StepOutcome, error) {
		return StepOutcome{
			Output: "结果",
			Facts:  []Fact{{Key: "1.x", Value: "y"}},
		}, nil
	}
	run, err := NewPlanner(nil).Execute(t.Context(), plan, exec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(run.Results) != 1 {
		t.Fatalf("results=%d", len(run.Results))
	}
	r := run.Results[0]
	if len(r.Outcome.Facts) != 1 || r.Outcome.Facts[0].Key != "1.x" {
		t.Errorf("outcome not carried: %+v", r.Outcome)
	}
	if r.Attempt != 1 || r.Duration < 0 {
		t.Errorf("attempt=%d duration=%d", r.Attempt, r.Duration)
	}
}
