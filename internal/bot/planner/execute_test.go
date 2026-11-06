package planner

import (
	"context"
	"fmt"
	"sort"
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

// TestExecute_GoldenInvalidationPropagation 对应 docs/planner-v2-spec.md §15.4。
//
// 8 步计划里，step2 断言“文件是 CSV”，step7 复核时发现“实际是 XLSX”。
// 期望只作废 2→3→7 这条依赖链，4/5/6/8 的结果原样复用，并且只重规划一次。
// 这个用例把“失效传播”从一句设计描述变成可逐字段验证的行为契约。
func TestExecute_GoldenInvalidationPropagation(t *testing.T) {
	plan := testPlan(t,
		PlanStep{ID: 1, Description: "理解用户问题"},
		PlanStep{ID: 2, Description: "判断文件格式为 CSV"},
		PlanStep{ID: 3, Description: "按 CSV 解析文件", DependsOn: []int{2}},
		PlanStep{ID: 4, Description: "检索字段含义"},
		PlanStep{ID: 5, Description: "生成初步统计图"},
		PlanStep{ID: 6, Description: "整理术语表"},
		PlanStep{ID: 7, Description: "复核解析结果", DependsOn: []int{3}},
		PlanStep{ID: 8, Description: "整理报告素材", DependsOn: []int{4, 5, 6}},
	)

	// 1. 失效能否传播，取决于 2→3→7 这条路径真实存在。
	if got := plan.Step(3).DependsOn; len(got) != 1 || got[0] != 2 {
		t.Fatalf("step3 应依赖 step2，got %v", got)
	}
	if got := plan.Step(7).DependsOn; len(got) != 1 || got[0] != 3 {
		t.Fatalf("step7 应依赖 step3，got %v", got)
	}

	patch := `{"reasoning":"格式判断错误，改为按 XLSX 重新解析","operations":[
		{"op":"replace_from","id":2,"steps":[
			{"local_id":"a","description":"按 XLSX 重新解析文件","depends_on":[],"effect":"none"},
			{"local_id":"b","description":"展开合并单元格","depends_on":["a"],"effect":"none"}]}]}`
	chat := &fakeChat{responses: []string{patch, "最终答案"}}
	p := NewPlanner(chat.call)

	var mu sync.Mutex
	counts := map[int]int{}
	exec := func(ctx context.Context, step PlanStep, prior string) (StepOutcome, error) {
		mu.Lock()
		counts[step.ID]++
		mu.Unlock()
		switch step.ID {
		case 2:
			return StepOutcome{Output: "结论 OLD-CSV-CLAIM-4821"}, nil
		case 3:
			return StepOutcome{Output: "解析 OLD-CSV-PARSE-9137"}, nil
		case 7:
			// 7 报告的是 2 的前提不成立，而不是“3 执行失败”：
			// 失效集合必须是 [2]，而不是 [7]。
			return StepOutcome{
				Output: "复核 OLD-VERIFY-7731",
				Violations: []Violation{{
					StepID:   2,
					Claim:    "文件是 CSV",
					Reason:   "实际是 XLSX，含合并单元格",
					Evidence: "文件头 PK\\x03\\x04",
				}},
			}, nil
		case 9:
			return StepOutcome{Output: "新解析 NEW-XLSX-PARSE-5520"}, nil
		default:
			return StepOutcome{Output: fmt.Sprintf("out-%d", step.ID)}, nil
		}
	}

	var stale []int
	run, err := p.ExecuteWith(t.Context(), plan, exec, ExecuteOptions{
		Listener: func(ev ProgressEvent) {
			if ev.Type != EventStale {
				return
			}
			mu.Lock()
			stale = append(stale, ev.StepID)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if run.Status != RunCompleted {
		t.Fatalf("status=%s err=%v", run.Status, run.Err)
	}
	if run.Replans != 1 {
		t.Errorf("只应重规划一次，replans=%d", run.Replans)
	}

	// 2. 失效集合必须包含 2 及其下游 3、7，且不能把 7 当成种子反向吞掉 2。
	sort.Ints(stale)
	if fmt.Sprint(stale) != "[2 3 7]" {
		t.Errorf("stale 应为 [2 3 7]，got %v", stale)
	}

	// 3. 被替换的旧步骤保留审计痕迹（skipped），而不是凭空消失。
	for _, id := range []int{2, 3, 7} {
		if got := plan.Step(id).Status; got != StatusSkipped {
			t.Errorf("step%d 应为 skipped，got %s", id, got)
		}
	}
	for _, id := range []int{1, 4, 5, 6, 8, 9, 10} {
		if got := plan.Step(id).Status; got != StatusDone {
			t.Errorf("step%d 应为 done，got %s", id, got)
		}
	}

	// 4. 未受影响步骤只执行一次；旧链各执行一次后被替换，新链接上继续跑。
	for _, id := range []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10} {
		if counts[id] != 1 {
			t.Errorf("step%d 执行次数应为 1，got %d（counts=%v）", id, counts[id], counts)
		}
	}
	total := 0
	for _, n := range counts {
		total += n
	}
	if total != 10 {
		t.Errorf("总执行次数应为 原 8 步 + 新链 2 步 = 10，got %d", total)
	}

	// 5. violation 随结果保留，审计时能回答“是谁推翻了哪条结论”。
	var reported []Violation
	for _, r := range run.Results {
		if r.Step.ID == 7 {
			reported = r.Outcome.Violations
		}
	}
	if len(reported) != 1 || reported[0].StepID != 2 {
		t.Fatalf("step7 应保留指向 step2 的 violation，got %+v", reported)
	}

	// 6. 重规划 prompt 必须带准确失效集合，否则模型只能猜哪条链要重写。
	if len(chat.prompts) == 0 || !strings.Contains(chat.prompts[0], "失效集合：[2 3 7]") {
		t.Errorf("重规划 prompt 缺少失效集合：%v", chat.prompts)
	}

	// 7. 综合阶段不能再看到被推翻的正文；只允许最终有效结果进入 prompt。
	answer, err := p.Synthesize(t.Context(), plan.Goal, run.Results)
	if err != nil {
		t.Fatalf("synthesize: %v", err)
	}
	if answer != "最终答案" {
		t.Errorf("answer=%q", answer)
	}
	synthPrompt := chat.prompts[len(chat.prompts)-1]
	for _, old := range []string{"OLD-CSV-CLAIM-4821", "OLD-CSV-PARSE-9137", "OLD-VERIFY-7731"} {
		if strings.Contains(synthPrompt, old) {
			t.Errorf("综合 prompt 不应包含失效正文 %q：%s", old, synthPrompt)
		}
	}
	for _, want := range []string{"NEW-XLSX-PARSE-5520", "out-8"} {
		if !strings.Contains(synthPrompt, want) {
			t.Errorf("综合 prompt 缺少有效结果 %q：%s", want, synthPrompt)
		}
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
