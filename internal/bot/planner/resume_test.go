package planner

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// memRunStore 是并发安全的内存 RunStore。
// 续跑与步骤 checkpoint 会在并发层里读写，测试替身也必须加锁，
// 否则跑 -race 时会把产品代码里的正常并发误报成数据竞争。
type memRunStore struct {
	mu    sync.Mutex
	runs  map[string]*RunRecord
	steps []*StepRecord
}

func newMemRunStore() *memRunStore {
	return &memRunStore{runs: make(map[string]*RunRecord)}
}

func (s *memRunStore) CreateRun(_ context.Context, run *RunRecord) error {
	if run == nil {
		return errors.New("nil run")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *run
	cp.Plan = run.Plan.DeepCopy()
	s.runs[run.ID] = &cp
	return nil
}

func (s *memRunStore) UpdateRun(_ context.Context, runID string, patch RunPatch) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec := s.runs[runID]
	if rec == nil {
		return fmt.Errorf("run %s not found", runID)
	}
	if patch.Status != nil {
		rec.Status = *patch.Status
	}
	if patch.Revision != nil {
		rec.Revision = *patch.Revision
	}
	if patch.Plan != nil {
		rec.Plan = patch.Plan.DeepCopy()
	}
	if patch.Error != nil {
		rec.Error = *patch.Error
	}
	rec.UpdatedAt = time.Now()
	return nil
}

func (s *memRunStore) AppendStep(_ context.Context, rec *StepRecord) error {
	if rec == nil {
		return errors.New("nil step record")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *rec
	cp.Facts = append([]Fact(nil), rec.Facts...)
	cp.Violations = append([]Violation(nil), rec.Violations...)
	s.steps = append(s.steps, &cp)
	return nil
}

func (s *memRunStore) UpdateStep(_ context.Context, rec *StepRecord) error {
	if rec == nil {
		return errors.New("nil step record")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.steps {
		if s.steps[i].ID != rec.ID {
			continue
		}
		cp := *rec
		cp.Facts = append([]Fact(nil), rec.Facts...)
		cp.Violations = append([]Violation(nil), rec.Violations...)
		s.steps[i] = &cp
		return nil
	}
	return fmt.Errorf("step %s not found", rec.ID)
}

func (s *memRunStore) FindStepOutput(_ context.Context, runID string, stepID int, inputHash string) (*StepRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.steps) - 1; i >= 0; i-- {
		rec := s.steps[i]
		if rec.RunID == runID && rec.StepID == stepID &&
			rec.InputHash == inputHash && rec.Status == StatusDone {
			return rec, nil
		}
	}
	return nil, nil
}

func (s *memRunStore) GetRun(_ context.Context, runID string) (*RunRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec := s.runs[runID]
	if rec == nil {
		return nil, nil
	}
	cp := *rec
	cp.Plan = rec.Plan.DeepCopy()
	return &cp, nil
}

func (s *memRunStore) LatestRun(_ context.Context, conversationID string) (*RunRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var latest *RunRecord
	for _, rec := range s.runs {
		if rec.ConversationID != conversationID {
			continue
		}
		if latest == nil || rec.UpdatedAt.After(latest.UpdatedAt) {
			latest = rec
		}
	}
	if latest == nil {
		return nil, nil
	}
	cp := *latest
	cp.Plan = latest.Plan.DeepCopy()
	return &cp, nil
}

func (s *memRunStore) ListSteps(_ context.Context, runID string) ([]*StepRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*StepRecord
	for _, rec := range s.steps {
		if rec.RunID == runID {
			out = append(out, rec)
		}
	}
	return out, nil
}

func seedRun(t *testing.T, store *memRunStore, plan *Plan, id string) *RunRecord {
	t.Helper()
	now := time.Now()
	rec := &RunRecord{
		ID:        id,
		Goal:      plan.Goal,
		GoalHash:  GoalHash(plan.Goal),
		Status:    RunRunning,
		Revision:  plan.Revision,
		Plan:      plan.DeepCopy(),
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.CreateRun(t.Context(), rec); err != nil {
		t.Fatalf("seed run: %v", err)
	}
	return rec
}

func TestExecuteWith_ReplaysCompletedSideEffect(t *testing.T) {
	plan := testPlan(t, PlanStep{
		ID: 1, Description: "发送通知", Effect: EffectSideEffect,
	})
	store := newMemRunStore()
	seedRun(t, store, plan, "run-replay")

	// 上一次运行已经发出通知，输入指纹与本次一致。
	inputHash := StepInputHash(plan.Steps[0], "")
	if err := store.AppendStep(t.Context(), &StepRecord{
		ID: "step-1", RunID: "run-replay", StepID: 1, Attempt: 1,
		Status: StatusDone, Effect: EffectSideEffect, InputHash: inputHash,
		Output: "已发送", DurationMs: 18,
	}); err != nil {
		t.Fatalf("seed step: %v", err)
	}

	var calls atomic.Int32
	exec := func(context.Context, PlanStep, string) (StepOutcome, error) {
		calls.Add(1)
		return StepOutcome{Output: "重复发送"}, nil
	}
	run, err := NewPlanner(nil).ExecuteWith(t.Context(), plan, exec, ExecuteOptions{
		Store: store,
		Meta:  RunMeta{ResumeRunID: "run-replay"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls.Load() != 0 {
		t.Errorf("副作用步骤不应再次执行，calls=%d", calls.Load())
	}
	if run.ReplayedSteps != 1 || run.Results[0].Output != "已发送" {
		t.Errorf("replayed=%d output=%q", run.ReplayedSteps, run.Results[0].Output)
	}
}

func TestExecuteWith_ResumeSkipsCompletedReadOnlySteps(t *testing.T) {
	plan := testPlan(t,
		PlanStep{ID: 1, Description: "读取资料"},
		PlanStep{ID: 2, Description: "整理要点", DependsOn: []int{1}},
		PlanStep{ID: 3, Description: "撰写结论", DependsOn: []int{2}},
	)
	store := newMemRunStore()
	seedRun(t, store, plan, "run-resume")
	if err := store.AppendStep(t.Context(), &StepRecord{
		ID: "step-1", RunID: "run-resume", StepID: 1, Attempt: 1,
		Status: StatusDone, Effect: EffectNone, InputHash: "old",
		Output: "资料内容", Facts: []Fact{{Key: "1.doc", Value: "d_1"}},
	}); err != nil {
		t.Fatalf("seed step: %v", err)
	}

	var mu sync.Mutex
	var called []int
	exec := func(_ context.Context, step PlanStep, _ string) (StepOutcome, error) {
		mu.Lock()
		called = append(called, step.ID)
		mu.Unlock()
		return StepOutcome{Output: fmt.Sprintf("out-%d", step.ID)}, nil
	}
	run, err := NewPlanner(nil).ExecuteWith(t.Context(), plan, exec, ExecuteOptions{
		Store: store,
		Meta:  RunMeta{ResumeRunID: "run-resume"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if run.Status != RunCompleted {
		t.Fatalf("status=%s err=%v", run.Status, run.Err)
	}
	if len(called) != 2 || called[0] != 2 || called[1] != 3 {
		t.Errorf("续跑应只执行剩余步骤，called=%v", called)
	}
	if run.Results[0].Output != "资料内容" {
		t.Errorf("已完成步骤结果未恢复：%q", run.Results[0].Output)
	}
}

func TestCreatePlanWithBudget_TokenExhaustedBeforeCall(t *testing.T) {
	chat := &fakeChat{responses: []string{validPlanJSON(1)}}
	p := NewPlanner(chat.call)
	budget := NewBudget(Config{MaxLLMCalls: 10, MaxTokens: 10})

	_, err := p.CreatePlanWithBudget(t.Context(), strings.Repeat("很长的目标", 30), budget)
	if !errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("expected budget error, got %v", err)
	}
	if chat.calls != 0 {
		t.Errorf("token 预算不足时不应调用模型，calls=%d", chat.calls)
	}
	if used := budget.Used(); used != 0 {
		t.Errorf("token 预留失败时应退还调用额度，used=%d", used)
	}
}

func TestExecuteWith_VerifyFailureIsFailOpen(t *testing.T) {
	plan := testPlan(t, PlanStep{
		ID: 1, Description: "总结文档", Assumptions: []string{"文档存在"},
		Expects: "输出三段摘要",
	})
	// 校验器返回非 JSON：按"校验不可用"放行，不能把观测故障变成业务失败。
	chat := &fakeChat{responses: []string{"not json", "not json"}}
	p := NewPlanner(chat.call)

	var calls atomic.Int32
	exec := func(context.Context, PlanStep, string) (StepOutcome, error) {
		calls.Add(1)
		return StepOutcome{Output: "摘要正文"}, nil
	}
	run, err := p.ExecuteWith(t.Context(), plan, exec, ExecuteOptions{
		VerifyAssumes: true,
		VerifyExpects: true,
	})
	if err != nil || run.Status != RunCompleted {
		t.Fatalf("status=%v err=%v", run.Status, err)
	}
	if calls.Load() != 1 || chat.calls != 2 {
		t.Errorf("calls=%d verify_calls=%d", calls.Load(), chat.calls)
	}
}

func TestExecuteWith_CancelStillWritesTerminalState(t *testing.T) {
	plan := testPlan(t, PlanStep{ID: 1, Description: "第一步"})
	store := newMemRunStore()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	run, err := NewPlanner(nil).ExecuteWith(ctx, plan,
		func(context.Context, PlanStep, string) (StepOutcome, error) {
			return StepOutcome{Output: "不应执行"}, nil
		}, ExecuteOptions{Store: store})
	if err == nil {
		t.Fatal("canceled context should return an error")
	}
	if run == nil || run.Status != RunFailed {
		t.Fatalf("run=%+v", run)
	}
	rec, getErr := store.GetRun(t.Context(), run.RunID)
	if getErr != nil {
		t.Fatalf("get run: %v", getErr)
	}
	if rec == nil || rec.Status != RunFailed || rec.UpdatedAt.IsZero() {
		t.Errorf("cancel 后终态未落库：%+v", rec)
	}
}
