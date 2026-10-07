package planner

import (
	"encoding/json"
	"fmt"
	"strings"

	"Logos/pkg/strutil"
)

// ==================== Plan-and-Execute 规划器 ====================
//
// 与纯 ReAct 的差异：
//   - ReAct：每一步都要 LLM 决策"下一步做什么"，长任务决策链长、
//     中途漂移无约束，且无法向用户展示"我打算怎么做"；
//   - Plan-and-Execute：先一次规划生成显式步骤清单（可展示、可审计），
//     逐步执行，失败时重规划（Replan）而非整体失败，最后综合作答。
//
// 每步执行仍复用带工具的 ReAct Agent——单步内的工具调用、
// 知识检索能力完整保留，规划只负责"任务分解"这一层。

// StepStatus 步骤运行状态
type StepStatus string

const (
	StatusPending StepStatus = "pending"
	StatusRunning StepStatus = "running"
	StatusDone    StepStatus = "done"
	StatusFailed  StepStatus = "failed"
	StatusSkipped StepStatus = "skipped" // 重规划后被新步骤取代
)

// PlanStep 计划中的一个步骤
type PlanStep struct {
	ID          int    `json:"id"`
	Description string `json:"description"`
	// Tool 建议使用的工具名（可选，仅提示，执行器可自行选择）
	Tool string `json:"tool,omitempty"`

	// 运行态（不参与 JSON 序列化）
	Status StepStatus `json:"-"`
	Output string     `json:"-"`
}

// Plan 任务计划
type Plan struct {
	Goal  string     `json:"goal"`
	Steps []PlanStep `json:"steps"`
}

const (
	MaxPlanSteps    = 8  // 计划步骤上限：过长说明任务该拆或工具不足
	MinPlanSteps    = 1
	MaxReplanCount  = 1  // 重规划次数上限：避免失败循环
	maxStepOutputKB = 16 // 单步结果进入后续上下文的截断上限
)

// ParsePlan 解析 LLM 输出的计划 JSON 并校验。
// 非法结构返回错误（调用方决定降级为普通对话，而非失败）。
func ParsePlan(raw string) (*Plan, error) {
	cleaned := strutil.ExtractJSON(raw)
	var plan Plan
	if err := json.Unmarshal([]byte(cleaned), &plan); err != nil {
		return nil, fmt.Errorf("计划 JSON 解析失败: %w", err)
	}
	if err := ValidatePlan(&plan); err != nil {
		return nil, err
	}
	return &plan, nil
}

// ValidatePlan 校验计划结构（纯函数，可单测）
func ValidatePlan(p *Plan) error {
	if p == nil {
		return fmt.Errorf("计划为空")
	}
	if strings.TrimSpace(p.Goal) == "" {
		return fmt.Errorf("计划缺少目标")
	}
	if len(p.Steps) < MinPlanSteps {
		return fmt.Errorf("计划没有步骤")
	}
	if len(p.Steps) > MaxPlanSteps {
		return fmt.Errorf("计划步骤数 %d 超过上限 %d", len(p.Steps), MaxPlanSteps)
	}
	seen := make(map[int]bool, len(p.Steps))
	for i, s := range p.Steps {
		if s.ID <= 0 {
			return fmt.Errorf("步骤 %d 的 id 非法: %d", i+1, s.ID)
		}
		if seen[s.ID] {
			return fmt.Errorf("步骤 id 重复: %d", s.ID)
		}
		seen[s.ID] = true
		if strings.TrimSpace(s.Description) == "" {
			return fmt.Errorf("步骤 %d 缺少描述", s.ID)
		}
	}
	return nil
}

// FormatPlan 渲染计划为用户可读文本（进度展示用）
func (p *Plan) FormatPlan() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("【任务规划】目标：%s\n", p.Goal))
	for _, s := range p.Steps {
		if s.Tool != "" {
			sb.WriteString(fmt.Sprintf("%d. %s（建议工具：%s）\n", s.ID, s.Description, s.Tool))
		} else {
			sb.WriteString(fmt.Sprintf("%d. %s\n", s.ID, s.Description))
		}
	}
	return sb.String()
}

// StepResult 单步执行结果（进入后续步骤上下文与综合阶段）
type StepResult struct {
	Step    PlanStep
	Output  string
	Success bool
}

// FormatResults 渲染已执行步骤及其结果（构建后续 prompt 用）
func FormatResults(results []StepResult) string {
	var sb strings.Builder
	for _, r := range results {
		status := "成功"
		if !r.Success {
			status = "失败"
		}
		out := r.Output
		if len(out) > maxStepOutputKB*1024 {
			out = out[:maxStepOutputKB*1024] + "...(截断)"
		}
		sb.WriteString(fmt.Sprintf("步骤 %d：%s【%s】\n结果：%s\n\n",
			r.Step.ID, r.Step.Description, status, out))
	}
	return sb.String()
}
