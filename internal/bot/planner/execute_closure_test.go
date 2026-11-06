package planner

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// TestExecuteWith_AssumptionViolationBlocksAndReplans 覆盖路径 B 的完整闭环：
// 前提被推翻 -> 步骤进入 blocked -> 上游失效 -> patch 改写 -> 只重跑受影响部分。
func TestExecuteWith_AssumptionViolationBlocksAndReplans(t *testing.T) {
	plan := testPlan(t,
		PlanStep{ID: 1, Description: "读取配置文件"},
		PlanStep{
			ID: 2, Description: "按 YAML 解析配置", DependsOn: []int{1},
			Assumptions: []string{"配置文件是 YAML 格式"},
		},
	)
	patch := `{"reasoning":"绕开不成立的前提","operations":[
		{"op":"replace_from","id":2,"steps":[
			{"local_id":"a","description":"按实际格式解析配置","depends_on":[1],"effect":"none"}]}]}`
	chat := &fakeChat{responses: []string{
		`{"holds":false,"reason":"文件实际是 JSON","violated":[{"index":1,"reason":"扩展名是 .json"}]}`,
		patch,
	}}

	var (
		mu     sync.Mutex
		counts = map[int]int{}
		events []string
	)
	exec := func(ctx context.Context, step PlanStep, prior string) (StepOutcome, error) {
		mu.Lock()
		counts[step.ID]++
		mu.Unlock()
		return StepOutcome{Output: fmt.Sprintf("out-%d", step.ID)}, nil
	}
	listener := ProgressListenerV2(func(ev ProgressEvent) {
		mu.Lock()
		events = append(events, ev.Type)
		mu.Unlock()
	})

	run, err := NewPlanner(chat.call).ExecuteWith(t.Context(), plan, exec, ExecuteOptions{
		VerifyAssumes: true,
		Listener:      listener,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if run.Status != RunCompleted || run.Replans != 1 {
		t.Fatalf("status=%s replans=%d err=%v", run.Status, run.Replans, run.Err)
	}

	mu.Lock()
	defer mu.Unlock()
	if counts[2] != 0 {
		t.Errorf("前提不成立的步骤不应执行，counts=%v", counts)
	}
	if counts[1] != 2 {
		t.Errorf("上游被 violation 指向后应重跑一次，counts=%v", counts)
	}
	if len(counts) != 2 {
		t.Errorf("patch 后的替代步骤应执行，counts=%v", counts)
	}
	if !containsString(events, EventStepBlocked) {
		t.Errorf("缺少 %s 事件: %v", EventStepBlocked, events)
	}
	if !containsString(events, EventReplan) {
		t.Errorf("缺少 %s 事件: %v", EventReplan, events)
	}
	if len(chat.prompts) < 2 || !strings.Contains(chat.prompts[0], "配置文件是 YAML 格式") {
		t.Errorf("前提校验 prompt 应包含步骤前提，calls=%d", len(chat.prompts))
	}
}

// TestExecuteWith_RejectsOversizedInitialPlan 守住单次生成的 8 步入口约束。
// 直接调用 ExecuteWith 也必须被拦住，不能只依赖 ParsePlan。
func TestExecuteWith_RejectsOversizedInitialPlan(t *testing.T) {
	plan := &Plan{Goal: "九步计划", Steps: makeSteps(9)}
	for i := range plan.Steps {
		plan.Steps[i].Effect = EffectNone
	}
	store := newMemRunStore()

	run, err := NewPlanner(nil).ExecuteWith(t.Context(), plan,
		func(context.Context, PlanStep, string) (StepOutcome, error) {
			return StepOutcome{Output: "ok"}, nil
		},
		ExecuteOptions{Store: store},
	)
	if err == nil {
		t.Fatal("超过初始步数上限时应拒绝执行")
	}
	if !strings.Contains(err.Error(), "超过上限") {
		t.Errorf("错误应说明步数上限，got %v", err)
	}
	if run != nil {
		t.Errorf("入口校验失败时应返回 nil 结果，got %+v", run.Status)
	}
	if len(store.runs) != 0 {
		t.Errorf("入口校验失败时不应创建运行记录，runs=%d", len(store.runs))
	}
}

// TestExecuteWith_ResumeAllowsPatchedStepCount 验证续跑不会被初始 8 步上限误伤：
// patch 累积到 9 步的计划仍应能恢复并跑完。
func TestExecuteWith_ResumeAllowsPatchedStepCount(t *testing.T) {
	plan := &Plan{Goal: "续跑九步计划", Steps: makeSteps(9)}
	for i := range plan.Steps {
		plan.Steps[i].Effect = EffectNone
	}
	store := newMemRunStore()
	seedRun(t, store, plan, "run-nine")

	run, err := NewPlanner(nil).ExecuteWith(t.Context(), plan,
		func(context.Context, PlanStep, string) (StepOutcome, error) {
			return StepOutcome{Output: "ok"}, nil
		},
		ExecuteOptions{
			Store:  store,
			Config: Config{DisableReplan: true},
			Meta:   RunMeta{ResumeRunID: "run-nine"},
		},
	)
	if err != nil {
		t.Fatalf("9 步续跑不应被拒绝: %v", err)
	}
	if run.Status != RunCompleted {
		t.Fatalf("status=%s err=%v", run.Status, run.Err)
	}
	if run.RunID != "run-nine" {
		t.Errorf("应复用续跑 run_id，got %s", run.RunID)
	}
}

// TestExecuteWith_CheckpointUpdatesSameRow 验证 running/final 合并成一行：
// 否则每次续跑都会读到一条永远 running 的脏记录。
func TestExecuteWith_CheckpointUpdatesSameRow(t *testing.T) {
	plan := testPlan(t,
		PlanStep{ID: 1, Description: "第一步"},
		PlanStep{ID: 2, Description: "第二步"},
	)
	store := newMemRunStore()

	run, err := NewPlanner(nil).ExecuteWith(t.Context(), plan,
		func(context.Context, PlanStep, string) (StepOutcome, error) {
			return StepOutcome{Output: "ok"}, nil
		},
		ExecuteOptions{Store: store, Config: Config{DisableReplan: true}},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if run.Status != RunCompleted {
		t.Fatalf("status=%s err=%v", run.Status, run.Err)
	}

	records, err := store.ListSteps(t.Context(), run.RunID)
	if err != nil {
		t.Fatalf("list steps: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("每步应只有一行 checkpoint，got %d", len(records))
	}
	for _, rec := range records {
		if rec.Status != StatusDone {
			t.Errorf("step %d 终态应为 done，got %s", rec.StepID, rec.Status)
		}
		if rec.StartedAt.IsZero() || rec.FinishedAt.IsZero() {
			t.Errorf("step %d 缺少开始/结束时间: %+v", rec.StepID, rec)
		}
	}
}

// TestNextDegrade_FollowsLadder 验证降级顺序与"只降不升"。
func TestNextDegrade_FollowsLadder(t *testing.T) {
	st := &execState{verifyExpects: true}
	cfg := Config{MaxConcurrency: 4, MaxReplans: 2}

	wantStages := []int{degradeConcurrency, degradeVerify, degradeReplan, degradePartial, degradePartial}
	for i, want := range wantStages {
		if got := st.nextDegrade(&cfg); got != want {
			t.Fatalf("第 %d 次降级=%d，期望 %d", i+1, got, want)
		}
		switch want {
		case degradeConcurrency:
			if cfg.MaxConcurrency != 1 {
				t.Errorf("第一级应把并发降到 1，got %d", cfg.MaxConcurrency)
			}
		case degradeVerify:
			if st.verifyExpects {
				t.Error("第二级应关闭路径 C")
			}
		case degradeReplan:
			if cfg.MaxReplans != 0 {
				t.Errorf("第三级应关闭重规划，got %d", cfg.MaxReplans)
			}
		}
	}
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
