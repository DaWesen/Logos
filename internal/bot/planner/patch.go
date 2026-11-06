package planner

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"Logos/pkg/strutil"
)

// ErrPatchRejected 表示 patch 未通过校验，计划已整体回滚。
//
// 用哨兵错误而不是字符串匹配，调用方可以用 errors.Is 区分
// "模型给的 patch 不合法"和"存储/网络失败"。
var ErrPatchRejected = errors.New("plan patch rejected")

// PlanPatch 是重规划输出：对现有计划的增量修改，而不是一份新计划。
// 整段重写会丢掉已完成步骤的结果，等于放弃增量执行的全部收益。
type PlanPatch struct {
	Reasoning  string    `json:"reasoning"`
	Operations []PatchOp `json:"operations"`
}

// PatchOp 单个 patch 操作。
type PatchOp struct {
	Op      string      `json:"op"`
	ID      int         `json:"id,omitempty"`
	LocalID string      `json:"local_id,omitempty"`
	Steps   []PatchStep `json:"steps,omitempty"`
	Patch   *StepPatch  `json:"patch,omitempty"`
}

// PatchStep 新增步骤。新增步骤只能带 local_id，全局 ID 由系统分配。
type PatchStep struct {
	LocalID     string       `json:"local_id"`
	Description string       `json:"description"`
	Tool        string       `json:"tool,omitempty"`
	DependsOn   []Dependency `json:"depends_on,omitempty"`
	Assumptions []string     `json:"assumptions,omitempty"`
	Expects     string       `json:"expects,omitempty"`
	Effect      EffectLevel  `json:"effect,omitempty"`
}

// StepPatch 对既有步骤的小幅修改。
// 指针用于区分"没传这个字段"和"传了空值"。
type StepPatch struct {
	Description *string       `json:"description,omitempty"`
	Tool        *string       `json:"tool,omitempty"`
	DependsOn   *[]Dependency `json:"depends_on,omitempty"`
	Assumptions *[]string     `json:"assumptions,omitempty"`
	Expects     *string       `json:"expects,omitempty"`
	Effect      *EffectLevel  `json:"effect,omitempty"`
}

// Dependency 兼容模型输出的两种写法：全局数字 ID 或本次 patch 的 local_id。
type Dependency struct {
	ID    int
	Local string
}

func (d *Dependency) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return fmt.Errorf("依赖为空")
	}
	if data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		d.Local = strings.TrimSpace(s)
		if d.Local == "" {
			return fmt.Errorf("local_id 为空")
		}
		return nil
	}
	var id int
	if err := json.Unmarshal(data, &id); err != nil {
		return fmt.Errorf("依赖必须是数字 ID 或 local_id 字符串: %w", err)
	}
	if id <= 0 {
		return fmt.Errorf("依赖 ID 非法: %d", id)
	}
	d.ID = id
	return nil
}

func (d Dependency) MarshalJSON() ([]byte, error) {
	if d.Local != "" {
		return json.Marshal(d.Local)
	}
	return json.Marshal(d.ID)
}

func (d Dependency) String() string {
	if d.Local != "" {
		return d.Local
	}
	return fmt.Sprintf("%d", d.ID)
}

// ParsePlanPatch 解析重规划输出。
// 只做语法解析，操作语义与引用完整性统一由 ApplyPatch 在副本上校验。
func ParsePlanPatch(raw string) (*PlanPatch, error) {
	cleaned := strutil.ExtractJSON(raw)
	var patch PlanPatch
	if err := json.Unmarshal([]byte(cleaned), &patch); err != nil {
		return nil, fmt.Errorf("patch JSON 解析失败: %w", err)
	}
	if len(patch.Operations) == 0 {
		return nil, fmt.Errorf("%w: 没有任何操作", ErrPatchRejected)
	}
	return &patch, nil
}

// ApplyPatch 在副本上应用 patch，校验通过后整体替换原计划。
//
// 绝不"先改再改回来"：Steps 切片会被多个读路径同时持有，
// 原地回滚最容易留下"改了一半"的脏状态。
func (p *Plan) ApplyPatch(patch *PlanPatch) error {
	_, err := p.ApplyPatchTracked(patch)
	return err
}

// ApplyPatchTracked 与 ApplyPatch 相同，但返回受影响步骤的全局 ID 集合。
func (p *Plan) ApplyPatchTracked(patch *PlanPatch) ([]int, error) {
	if p == nil {
		return nil, fmt.Errorf("%w: 计划为空", ErrPatchRejected)
	}
	if patch == nil || len(patch.Operations) == 0 {
		return nil, fmt.Errorf("%w: 没有任何操作", ErrPatchRejected)
	}

	clone := p.DeepCopy()
	if clone.nextID <= 0 {
		maxID := 0
		for _, s := range clone.Steps {
			if s.ID > maxID {
				maxID = s.ID
			}
		}
		clone.nextID = maxID + 1
	}

	local := make(map[string]int)
	// 先为所有新增步骤分配全局 ID，这样后续操作可以跨 op 引用 local_id。
	for _, op := range patch.Operations {
		switch op.Op {
		case "add", "replace_from":
			for _, st := range op.Steps {
				lid := strings.TrimSpace(st.LocalID)
				if lid == "" {
					return nil, fmt.Errorf("%w: %s 的新步骤缺少 local_id", ErrPatchRejected, op.Op)
				}
				if _, exists := local[lid]; exists {
					return nil, fmt.Errorf("%w: local_id 重复: %s", ErrPatchRejected, lid)
				}
				local[lid] = clone.nextID
				clone.nextID++
			}
		}
	}

	// 累积后总数不能超过 MaxTotalSteps；先算一遍，避免分配了 ID 才发现超限。
	newCount := 0
	for _, op := range patch.Operations {
		if op.Op == "add" || op.Op == "replace_from" {
			newCount += len(op.Steps)
		}
	}
	if len(clone.Steps)+newCount > MaxTotalSteps {
		return nil, fmt.Errorf("%w: patch 后步骤数 %d 超过上限 %d",
			ErrPatchRejected, len(clone.Steps)+newCount, MaxTotalSteps)
	}

	affected := make(map[int]bool)
	for _, op := range patch.Operations {
		switch op.Op {
		case "add":
			steps, err := buildPatchSteps(op.Steps, local, clone, nil)
			if err != nil {
				return nil, err
			}
			for _, s := range steps {
				clone.Steps = append(clone.Steps, s)
				affected[s.ID] = true
			}

		case "modify":
			target := clone.Step(op.ID)
			if target == nil {
				return nil, fmt.Errorf("%w: modify 指向不存在的步骤 %d", ErrPatchRejected, op.ID)
			}
			if op.Patch == nil {
				return nil, fmt.Errorf("%w: modify 缺少 patch 内容", ErrPatchRejected)
			}
			if op.Patch.Effect != nil {
				// 降级副作用等级必须走单独审计接口，不能由模型一句话决定。
				return nil, fmt.Errorf("%w: modify 不允许修改 effect", ErrPatchRejected)
			}
			if op.Patch.Description != nil {
				target.Description = strings.TrimSpace(*op.Patch.Description)
			}
			if op.Patch.Tool != nil {
				target.Tool = strings.TrimSpace(*op.Patch.Tool)
			}
			if op.Patch.Assumptions != nil {
				target.Assumptions = append([]string(nil), (*op.Patch.Assumptions)...)
			}
			if op.Patch.Expects != nil {
				target.Expects = strings.TrimSpace(*op.Patch.Expects)
			}
			if op.Patch.DependsOn != nil {
				deps, err := resolveDependencies(*op.Patch.DependsOn, clone, local)
				if err != nil {
					return nil, err
				}
				target.DependsOn = deps
			}
			affected[target.ID] = true

		case "remove":
			target := clone.Step(op.ID)
			if target == nil {
				return nil, fmt.Errorf("%w: remove 指向不存在的步骤 %d", ErrPatchRejected, op.ID)
			}
			victims := clone.Propagate([]int{op.ID})
			for _, id := range victims {
				if err := skipForPatch(clone.Step(id), "被 patch 移除"); err != nil {
					return nil, err
				}
				affected[id] = true
			}

		case "replace_from":
			target := clone.Step(op.ID)
			if target == nil {
				return nil, fmt.Errorf("%w: replace_from 指向不存在的步骤 %d", ErrPatchRejected, op.ID)
			}
			inherited := append([]int(nil), target.DependsOn...)
			victims := clone.Propagate([]int{op.ID})
			for _, id := range victims {
				if err := skipForPatch(clone.Step(id), "被 patch 替换"); err != nil {
					return nil, err
				}
				affected[id] = true
			}
			steps, err := buildPatchSteps(op.Steps, local, clone, inherited)
			if err != nil {
				return nil, err
			}
			for _, s := range steps {
				clone.Steps = append(clone.Steps, s)
				affected[s.ID] = true
			}

		default:
			return nil, fmt.Errorf("%w: 不支持的操作 %q", ErrPatchRejected, op.Op)
		}
	}

	if err := validatePlan(clone, MaxTotalSteps); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPatchRejected, err)
	}

	*p = *clone
	out := make([]int, 0, len(affected))
	for id := range affected {
		out = append(out, id)
	}
	sort.Ints(out)
	return out, nil
}

// skipForPatch 通过状态机把步骤标记为 skipped。
//
// done 不能直接跳到 skipped；先迁移到 stale 表示"结果已作废"，
// 再迁移到 skipped 表示"已从计划中移除"。这样审计记录里仍能看出
// 该步骤曾经成功执行过，而不是凭空消失。
func skipForPatch(step *PlanStep, reason string) error {
	if step == nil || step.Status == StatusSkipped {
		return nil
	}
	if step.Status == StatusDone {
		if err := step.Transition(StatusStale); err != nil {
			return fmt.Errorf("%w: %v", ErrPatchRejected, err)
		}
	}
	if err := step.Transition(StatusSkipped); err != nil {
		return fmt.Errorf("%w: %v", ErrPatchRejected, err)
	}
	step.StaleReason = reason
	return nil
}

func buildPatchSteps(steps []PatchStep, local map[string]int, plan *Plan, inherited []int) ([]PlanStep, error) {
	if len(steps) == 0 {
		return nil, fmt.Errorf("%w: 操作没有提供新步骤", ErrPatchRejected)
	}
	out := make([]PlanStep, 0, len(steps))
	for i, st := range steps {
		lid := strings.TrimSpace(st.LocalID)
		id, ok := local[lid]
		if !ok {
			return nil, fmt.Errorf("%w: 未知 local_id %q", ErrPatchRejected, lid)
		}
		deps, err := resolveDependencies(st.DependsOn, plan, local)
		if err != nil {
			return nil, err
		}
		if i == 0 && len(inherited) > 0 {
			deps = dedupeIDs(append(deps, inherited...))
		}
		effect := st.Effect
		if effect == "" {
			effect = EffectNone
		}
		out = append(out, PlanStep{
			ID:          id,
			Description: strings.TrimSpace(st.Description),
			Tool:        strings.TrimSpace(st.Tool),
			DependsOn:   deps,
			Assumptions: append([]string(nil), st.Assumptions...),
			Expects:     strings.TrimSpace(st.Expects),
			Effect:      effect,
			Status:      StatusPending,
		})
	}
	return out, nil
}

func resolveDependencies(deps []Dependency, plan *Plan, local map[string]int) ([]int, error) {
	if len(deps) == 0 {
		return nil, nil
	}
	out := make([]int, 0, len(deps))
	for _, dep := range deps {
		if dep.Local != "" {
			id, ok := local[dep.Local]
			if !ok {
				return nil, fmt.Errorf("%w: 依赖引用未知 local_id %q", ErrPatchRejected, dep.Local)
			}
			out = append(out, id)
			continue
		}
		if dep.ID <= 0 || plan.Step(dep.ID) == nil {
			return nil, fmt.Errorf("%w: 依赖引用不存在的步骤 %d", ErrPatchRejected, dep.ID)
		}
		out = append(out, dep.ID)
	}
	return dedupeIDs(out), nil
}
