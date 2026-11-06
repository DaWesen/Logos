package planner

import (
	"errors"
	"strings"
	"testing"
)

func TestApplyPatch_AddWithLocalDependencies(t *testing.T) {
	plan := testPlan(t, PlanStep{ID: 1, Description: "第一步"})
	patch := &PlanPatch{Operations: []PatchOp{{
		Op: "add",
		Steps: []PatchStep{
			{LocalID: "a", Description: "新增 A", DependsOn: []Dependency{{ID: 1}}},
			{LocalID: "b", Description: "新增 B", DependsOn: []Dependency{{Local: "a"}}},
		},
	}}}
	affected, err := plan.ApplyPatchTracked(patch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.Steps) != 3 {
		t.Fatalf("steps=%d", len(plan.Steps))
	}
	if plan.Steps[1].ID != 2 || plan.Steps[2].ID != 3 {
		t.Fatalf("新增步骤应由系统分配 2/3，got %d/%d", plan.Steps[1].ID, plan.Steps[2].ID)
	}
	if got := plan.Steps[1].DependsOn; len(got) != 1 || got[0] != 1 {
		t.Errorf("A deps=%v", got)
	}
	if got := plan.Steps[2].DependsOn; len(got) != 1 || got[0] != 2 {
		t.Errorf("B deps=%v", got)
	}
	if len(affected) != 2 {
		t.Errorf("affected=%v", affected)
	}
}

func TestApplyPatch_CycleRollsBackWholePlan(t *testing.T) {
	plan := testPlan(t,
		PlanStep{ID: 1, Description: "第一步"},
		PlanStep{ID: 2, Description: "第二步", DependsOn: []int{1}},
	)
	before := plan.DeepCopy()
	patch := &PlanPatch{Operations: []PatchOp{{
		Op: "modify", ID: 1,
		Patch: &StepPatch{DependsOn: &[]Dependency{{ID: 2}}},
	}}}
	err := plan.ApplyPatch(patch)
	if !errors.Is(err, ErrPatchRejected) {
		t.Fatalf("want ErrPatchRejected, got %v", err)
	}
	if len(plan.Steps) != len(before.Steps) {
		t.Fatalf("steps changed after rollback")
	}
	for i := range plan.Steps {
		if plan.Steps[i].Description != before.Steps[i].Description ||
			len(plan.Steps[i].DependsOn) != len(before.Steps[i].DependsOn) {
			t.Fatalf("plan mutated after rejected patch: %+v", plan.Steps)
		}
	}
}

func TestApplyPatch_ModifyEffectRejected(t *testing.T) {
	plan := testPlan(t, PlanStep{ID: 1, Description: "发送消息", Effect: EffectSideEffect})
	effect := EffectNone
	err := plan.ApplyPatch(&PlanPatch{Operations: []PatchOp{{
		Op: "modify", ID: 1, Patch: &StepPatch{Effect: &effect},
	}}})
	if !errors.Is(err, ErrPatchRejected) {
		t.Fatalf("want ErrPatchRejected, got %v", err)
	}
	if plan.Steps[0].Effect != EffectSideEffect {
		t.Errorf("effect 不应被模型降级，got %s", plan.Steps[0].Effect)
	}
}

func TestApplyPatch_RemoveSkipsDownstream(t *testing.T) {
	plan := testPlan(t,
		PlanStep{ID: 1, Description: "一", Status: StatusDone},
		PlanStep{ID: 2, Description: "二", DependsOn: []int{1}, Status: StatusDone},
		PlanStep{ID: 3, Description: "三", DependsOn: []int{2}, Status: StatusPending},
		PlanStep{ID: 4, Description: "独立", Status: StatusPending},
	)
	if err := plan.ApplyPatch(&PlanPatch{Operations: []PatchOp{{Op: "remove", ID: 2}}}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, id := range []int{2, 3} {
		if plan.Step(id).Status != StatusSkipped {
			t.Errorf("step %d should be skipped, got %s", id, plan.Step(id).Status)
		}
	}
	if plan.Step(4).Status == StatusSkipped {
		t.Error("无关步骤不应被跳过")
	}
}

func TestParsePlanPatch_RejectsEmptyOperations(t *testing.T) {
	_, err := ParsePlanPatch(`{"reasoning":"x","operations":[]}`)
	if !errors.Is(err, ErrPatchRejected) {
		t.Fatalf("want ErrPatchRejected, got %v", err)
	}
}

func TestApplyPatch_UnknownOperationKeepsPlan(t *testing.T) {
	plan := testPlan(t, PlanStep{ID: 1, Description: "一"})
	err := plan.ApplyPatch(&PlanPatch{Operations: []PatchOp{{Op: "reset"}}})
	if !errors.Is(err, ErrPatchRejected) || !strings.Contains(err.Error(), "不支持") {
		t.Fatalf("want rejected unsupported op, got %v", err)
	}
	if len(plan.Steps) != 1 {
		t.Fatal("plan changed")
	}
}
