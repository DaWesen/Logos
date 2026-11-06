package planner

import (
	"context"
	"errors"
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

// TestExecute_UnknownViolationIsIgnored 覆盖 T6。
// 模型编造 step_id 时只告警并继续：一条格式幻觉不应该让整轮已完成的结果作废。
func TestExecute_UnknownViolationIsIgnored(t *testing.T) {
	plan := testPlan(t,
		PlanStep{ID: 1, Description: "第一步"},
		PlanStep{ID: 2, Description: "第二步", DependsOn: []int{1}},
	)
	exec := func(ctx context.Context, step PlanStep, prior string) (StepOutcome, error) {
		out := StepOutcome{Output: fmt.Sprintf("out-%d", step.ID)}
		if step.ID == 2 {
			out.Violations = []Violation{{StepID: 99, Claim: "不存在的步骤"}}
		}
		return out, nil
	}

	var warnings []string
	var mu sync.Mutex
	run, err := NewPlanner(nil).ExecuteWith(t.Context(), plan, exec, ExecuteOptions{
		Warnf: func(format string, args ...any) {
			mu.Lock()
			warnings = append(warnings, fmt.Sprintf(format, args...))
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if run.Status != RunCompleted {
		t.Fatalf("status=%s err=%v", run.Status, run.Err)
	}
	if !plan.AllDone() {
		t.Errorf("非法 violation 不应让步骤失效: %+v", plan.Steps)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(warnings) != 1 || !strings.Contains(warnings[0], "不存在") {
		t.Errorf("应记录一条可恢复告警，got %v", warnings)
	}
}

// TestExecute_ReplanBudgetTerminatesDegraded 覆盖 T9（同时验证永久失败不重试）。
// 连续失败时必须耗尽重规划配额后收口为 degraded，既不能无限打补丁，
// 也不能把已经完成的部分结果丢掉。
func TestExecute_ReplanBudgetTerminatesDegraded(t *testing.T) {
	plan := testPlan(t, PlanStep{ID: 1, Description: "第一步"})
	chat := &fakeChat{responses: []string{
		`{"reasoning":"换一条链","operations":[
			{"op":"remove","id":1},
			{"op":"add","steps":[{"local_id":"a","description":"替代步骤 A","effect":"none"}]}]}`,
		`{"reasoning":"再换一条链","operations":[
			{"op":"remove","id":2},
			{"op":"add","steps":[{"local_id":"b","description":"替代步骤 B","effect":"none"}]}]}`,
	}}
	p := NewPlanner(chat.call)

	var executions atomic.Int32
	exec := func(ctx context.Context, step PlanStep, prior string) (StepOutcome, error) {
		executions.Add(1)
		return StepOutcome{}, ErrPermission
	}
	run, err := p.ExecuteWith(t.Context(), plan, exec, ExecuteOptions{
		Config: Config{MaxReplans: 2, MaxRetries: 0},
	})
	if err != nil {
		t.Fatalf("degraded run should return partial results: %v", err)
	}
	if run.Status != RunDegraded {
		t.Fatalf("status=%s err=%v", run.Status, run.Err)
	}
	if run.Replans != 2 {
		t.Errorf("应恰好耗尽 2 次重规划，got %d", run.Replans)
	}
	if chat.calls != 2 {
		t.Errorf("重规划调用次数应等于配额，got %d", chat.calls)
	}
	if executions.Load() != 3 {
		t.Errorf("永久失败不应重试，3 个步骤各执行一次，got %d", executions.Load())
	}
	if got := plan.Step(3).Status; got != StatusStale {
		t.Errorf("最后一个失败步骤应标 stale，got %s", got)
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

// TestExecute_TokenBudgetCoversStepPrompt 保证 token 预算不是"只统计规划器"。
// 单步 prompt 都放不下时必须在调用 Agent 前终止，不能先花掉一次真实调用
// 再在事后统计里发现超支。
func TestExecute_TokenBudgetCoversStepPrompt(t *testing.T) {
	plan := testPlan(t, PlanStep{ID: 1, Description: "读取一个很长的文档"})
	var calls atomic.Int32
	exec := func(ctx context.Context, step PlanStep, prior string) (StepOutcome, error) {
		calls.Add(1)
		return StepOutcome{Output: "ok"}, nil
	}
	run, err := NewPlanner(nil).ExecuteWith(t.Context(), plan, exec, ExecuteOptions{
		Config: Config{MaxLLMCalls: 10, MaxTokens: 10, DisableReplan: true},
	})
	if err == nil {
		t.Fatal("token 预算不足时应返回错误供上层降级")
	}
	if run.Status != RunBudgetExhausted {
		t.Fatalf("status=%s", run.Status)
	}
	if calls.Load() != 0 {
		t.Errorf("预算不足时不应调用执行器，calls=%d", calls.Load())
	}
}

// TestExecute_StepTokensEnterSharedBudget 验证执行成本与规划成本共用一本账。
// 面试里能讲清楚"预算覆盖全链路"的前提，是这里真的有一致口径的计数。
func TestExecute_StepTokensEnterSharedBudget(t *testing.T) {
	plan := testPlan(t, PlanStep{ID: 1, Description: "执行一步"})
	budget := NewBudget(Config{MaxTokens: 100_000})
	exec := func(ctx context.Context, step PlanStep, prior string) (StepOutcome, error) {
		return StepOutcome{Output: "一段足够长的执行结果，用于确认 token 记账不为零。"}, nil
	}
	run, err := NewPlanner(nil).ExecuteWith(t.Context(), plan, exec, ExecuteOptions{
		Budget: budget,
		Config: Config{DisableReplan: true},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if budget.TokensUsed() <= 0 || run.TokensUsed != budget.TokensUsed() {
		t.Errorf("执行 token 应进入共享预算：budget=%d run=%d",
			budget.TokensUsed(), run.TokensUsed)
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

// TestExecute_ParallelLayerPreReserve 验证并发层不会"跑到一半才发现预算不够"。
// 3 个独立步骤只有 2 次调用额度时，应降级为串行：把剩余额度按顺序用完，
// 而不是并发起跑 2 步——后者会留下无法解释的半层状态。
func TestExecute_ParallelLayerPreReserve(t *testing.T) {
	plan := testPlan(t,
		PlanStep{ID: 1, Description: "一"},
		PlanStep{ID: 2, Description: "二"},
		PlanStep{ID: 3, Description: "三"},
	)
	var calls atomic.Int32
	var running, maxRunning atomic.Int32
	exec := func(ctx context.Context, step PlanStep, prior string) (StepOutcome, error) {
		cur := running.Add(1)
		for {
			old := maxRunning.Load()
			if cur <= old || maxRunning.CompareAndSwap(old, cur) {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
		calls.Add(1)
		running.Add(-1)
		return StepOutcome{Output: "ok"}, nil
	}

	run, err := NewPlanner(nil).ExecuteWith(t.Context(), plan, exec, ExecuteOptions{
		Config: Config{MaxLLMCalls: 2, MaxConcurrency: 3, DisableReplan: true},
	})
	if !errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("expected budget error, got %v", err)
	}
	if run.Status != RunBudgetExhausted {
		t.Fatalf("status=%s", run.Status)
	}
	if calls.Load() != 2 {
		t.Errorf("降级串行后应用完剩余 2 次额度，calls=%d", calls.Load())
	}
	if maxRunning.Load() != 1 {
		t.Errorf("整层预留失败后不应并发执行，max_running=%d", maxRunning.Load())
	}
	if run.LLMCalls != 2 {
		t.Errorf("llm_calls=%d", run.LLMCalls)
	}
}

// TestExecute_ParallelLayerChargedOnce 防止"整层预占 + 单步再扣"造成双扣。
// 预算刚好等于步骤数时必须全部完成。
func TestExecute_ParallelLayerChargedOnce(t *testing.T) {
	plan := testPlan(t,
		PlanStep{ID: 1, Description: "一"},
		PlanStep{ID: 2, Description: "二"},
		PlanStep{ID: 3, Description: "三"},
	)
	exec := func(ctx context.Context, step PlanStep, prior string) (StepOutcome, error) {
		return StepOutcome{Output: "ok"}, nil
	}
	run, err := NewPlanner(nil).ExecuteWith(t.Context(), plan, exec, ExecuteOptions{
		Config: Config{MaxLLMCalls: 3, MaxConcurrency: 3, DisableReplan: true},
	})
	if err != nil || run.Status != RunCompleted {
		t.Fatalf("status=%v err=%v", run.Status, err)
	}
	if run.LLMCalls != 3 {
		t.Errorf("预算恰好等于步骤数时应只扣 3 次，got %d", run.LLMCalls)
	}
}

// TestExecute_FailedStepErrorNotInSynthesisPrompt 失败步骤的错误文本
// 可能包含工具参数、内部地址，综合阶段只应看到"没有可用结果"。
func TestExecute_FailedStepErrorNotInSynthesisPrompt(t *testing.T) {
	const secret = "SECRET-INTERNAL-ENDPOINT-7788"
	plan := testPlan(t,
		PlanStep{ID: 1, Description: "调用上游"},
		PlanStep{ID: 2, Description: "整理结果", DependsOn: []int{1}},
	)
	exec := func(ctx context.Context, step PlanStep, prior string) (StepOutcome, error) {
		return StepOutcome{}, fmt.Errorf("upstream 500: %s", secret)
	}
	chat := &fakeChat{responses: []string{"最终答案"}}
	p := NewPlanner(chat.call)
	run, err := p.ExecuteWith(t.Context(), plan, exec, ExecuteOptions{
		Config: Config{DisableReplan: true},
	})
	if run == nil || run.Status != RunDegraded {
		t.Fatalf("失败计划应返回部分结果，run=%v err=%v", run, err)
	}

	if _, err := p.SynthesizeWithBudget(t.Context(), plan.Goal, run.Results, nil); err != nil {
		t.Fatalf("synthesize: %v", err)
	}
	if len(chat.prompts) != 1 {
		t.Fatalf("prompts=%d", len(chat.prompts))
	}
	prompt := chat.prompts[0]
	if strings.Contains(prompt, secret) {
		t.Errorf("综合 prompt 泄漏了失败错误正文：%s", prompt)
	}
	for _, want := range []string{plan.Goal, "已失效"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("综合 prompt 缺少 %q：%s", want, prompt)
		}
	}
}

// TestFormatSynthesisResults_FailedBodyOmitted 直接覆盖失败分支：
// 没有进入 stale 的失败结果也只能保留标题。
func TestFormatSynthesisResults_FailedBodyOmitted(t *testing.T) {
	results := []StepResult{
		{
			Step:    PlanStep{ID: 1, Description: "调用上游", Status: StatusFailed},
			Output:  "upstream 500: SECRET-BODY",
			Success: false,
			Err:     errors.New("upstream 500: SECRET-BODY"),
		},
		{
			Step:    PlanStep{ID: 2, Description: "有效步骤", Status: StatusDone},
			Output:  "有效正文",
			Success: true,
		},
	}
	got := FormatSynthesisResults(results)
	if strings.Contains(got, "SECRET-BODY") {
		t.Errorf("失败正文不应进入综合 prompt：%s", got)
	}
	for _, want := range []string{"调用上游", "失败/未完成", "有效正文"} {
		if !strings.Contains(got, want) {
			t.Errorf("缺少 %q：%s", want, got)
		}
	}
}

// TestExecute_StepReportAndTokenCheckpoint 覆盖"可观测性收口"：
// 每一步都要有结构化报告，checkpoint 要留下可解释的 token 估算。
func TestExecute_StepReportAndTokenCheckpoint(t *testing.T) {
	plan := testPlan(t,
		PlanStep{ID: 1, Description: "读取资料"},
		PlanStep{ID: 2, Description: "汇总结论", DependsOn: []int{1}},
	)
	store := newMemRunStore()
	var mu sync.Mutex
	var reports []StepReport
	exec := func(ctx context.Context, step PlanStep, prior string) (StepOutcome, error) {
		return StepOutcome{Output: fmt.Sprintf("步骤 %d 的产出内容", step.ID)}, nil
	}
	run, err := NewPlanner(nil).ExecuteWith(t.Context(), plan, exec, ExecuteOptions{
		Config: Config{DisableReplan: true},
		Store:  store,
		Meta:   RunMeta{BotID: "bot-1", ConversationID: "conv-1", UserID: "user-1"},
		OnStep: func(rep StepReport) {
			mu.Lock()
			reports = append(reports, rep)
			mu.Unlock()
		},
	})
	if err != nil || run.Status != RunCompleted {
		t.Fatalf("status=%v err=%v", run.Status, err)
	}
	mu.Lock()
	got := append([]StepReport(nil), reports...)
	mu.Unlock()
	if len(got) != 2 {
		t.Fatalf("每步应报告一次，got %d", len(got))
	}
	for _, rep := range got {
		if rep.RunID == "" || rep.StepID == 0 || rep.Status != StatusDone {
			t.Errorf("报告字段不完整：%+v", rep)
		}
		if rep.TokensIn <= 0 || rep.TokensOut <= 0 {
			t.Errorf("step%d token 估算应为正数：in=%d out=%d",
				rep.StepID, rep.TokensIn, rep.TokensOut)
		}
	}

	steps, err := store.ListSteps(t.Context(), run.RunID)
	if err != nil {
		t.Fatalf("list steps: %v", err)
	}
	var final *StepRecord
	for _, rec := range steps {
		if rec.StepID == 2 && rec.Status == StatusDone {
			final = rec
		}
	}
	if final == nil {
		t.Fatal("缺少 step2 的完成 checkpoint")
	}
	if final.TokensIn <= 0 || final.TokensOut <= 0 {
		t.Errorf("checkpoint token 估算应为正数：in=%d out=%d", final.TokensIn, final.TokensOut)
	}
}

// TestPlan_MarkStaleUsesStateMachine 确保批量失效标记不会把 pending
// 步骤直接改成 stale（那会让调度器失去"重新走 ready"的合法路径）。
func TestPlan_MarkStaleUsesStateMachine(t *testing.T) {
	plan := testPlan(t,
		PlanStep{ID: 1, Description: "已完成"},
		PlanStep{ID: 2, Description: "未开始"},
	)
	plan.Steps[0].Status = StatusDone
	plan.MarkStale([]int{1, 2}, "上游变更")
	if got := plan.Steps[0].Status; got != StatusStale {
		t.Errorf("done -> stale 应成功，got %s", got)
	}
	if got := plan.Steps[1].Status; got != StatusPending {
		t.Errorf("pending 不应被标为 stale，got %s", got)
	}
	plan.ResetStale()
	if got := plan.Steps[0].Status; got != StatusPending {
		t.Errorf("stale 应重置为 pending，got %s", got)
	}
}

// TestExecute_PatchModifiedDoneStepRerunsItAndDownstream 覆盖一个容易漏掉的边界：
// patch 修改的步骤可能不在本次失效集合里（例如 violation 指向的是下游），
// 但它已经产出的结果同样不能复用，必须连同下游一起重跑。
func TestExecute_PatchModifiedDoneStepRerunsItAndDownstream(t *testing.T) {
	plan := testPlan(t,
		PlanStep{ID: 1, Description: "读取原文件"},
		PlanStep{ID: 2, Description: "解析文件", DependsOn: []int{1}},
		PlanStep{ID: 3, Description: "生成报告", DependsOn: []int{2}},
	)
	patch := `{"reasoning":"读取方式需要调整","operations":[
		{"op":"modify","id":1,"patch":{"description":"重新读取原文件并探测编码"}}]}`
	chat := &fakeChat{responses: []string{patch}}
	p := NewPlanner(chat.call)

	var mu sync.Mutex
	counts := map[int]int{}
	exec := func(ctx context.Context, step PlanStep, prior string) (StepOutcome, error) {
		mu.Lock()
		counts[step.ID]++
		attempt := counts[step.ID]
		mu.Unlock()
		out := StepOutcome{Output: fmt.Sprintf("out-%d-v%d", step.ID, attempt)}
		if step.ID == 3 && attempt == 1 {
			out.Violations = []Violation{{
				StepID: 2, Claim: "解析口径错误", Reason: "需要按新的读取结果重新解析",
			}}
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
	for _, id := range []int{1, 2, 3} {
		if counts[id] != 2 {
			t.Errorf("step%d 应因定义变更重跑一次，counts=%v", id, counts)
		}
	}
	if plan.Step(1).Output != "out-1-v2" {
		t.Errorf("step1 应使用重跑结果，got %q", plan.Step(1).Output)
	}
	if !plan.AllDone() {
		t.Errorf("重跑后应全部完成：%+v", plan.Steps)
	}
}

func TestParseVerifyResponse_ProtocolCompatibility(t *testing.T) {
	tests := []struct {
		name       string
		resp       string
		wantOK     bool
		wantReason string
	}{
		{
			name:       "新协议 holds=false",
			resp:       `{"holds":false,"reason":"缺少权限"}`,
			wantOK:     false,
			wantReason: "缺少权限",
		},
		{
			name:   "新协议 holds=true",
			resp:   `{"holds":true}`,
			wantOK: true,
		},
		{
			name:       "旧协议 valid=false 仍然生效",
			resp:       `{"valid":false,"reason":"已过期"}`,
			wantOK:     false,
			wantReason: "已过期",
		},
		{
			name:   "字段缺失按校验不可用放行",
			resp:   `{"reason":"模型没给结论"}`,
			wantOK: true,
		},
		{
			name:   "非 JSON 放行",
			resp:   `看起来成立`,
			wantOK: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, reason := parseVerifyResponse(tt.resp)
			if ok != tt.wantOK {
				t.Errorf("ok=%v want=%v", ok, tt.wantOK)
			}
			if reason != tt.wantReason {
				t.Errorf("reason=%q want=%q", reason, tt.wantReason)
			}
		})
	}
}
