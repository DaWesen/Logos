package planner

import (
	"context"
	"fmt"
	"strings"
)

// ChatFunc 规划器专用的 LLM 调用（注入式，便于测试）
type ChatFunc func(ctx context.Context, systemPrompt, userPrompt string) (string, error)

const planSystemPrompt = `你是任务规划器。把用户目标分解为可顺序执行的步骤清单。
规则：
1. 步骤数在 1 到 %d 之间，每步是一个原子子任务，能用现有工具（知识库检索、图谱查询、MCP 工具等）或模型知识完成
2. 如果目标足够简单一步可完成，就只输出一个步骤
3. 每步的 description 要具体、自包含（执行者看不到其他上下文）
4. tool 字段填建议使用的工具名，没有合适工具就留空
输出 JSON，不要输出其他内容：
{"goal": "目标复述", "steps": [{"id": 1, "description": "步骤描述", "tool": "工具名或空"}]}`

const replanSystemPrompt = `你是任务规划器。之前的计划在执行中失败，需要修订。
规则：
1. 只输出尚未完成的剩余步骤（重新从 id 1 计数），已完成的步骤结果会提供给你参考
2. 针对失败原因调整方案：换工具、换角度，或拆分得更细
3. 输出 JSON，格式与原计划相同`

const synthesizeSystemPrompt = `你是任务综合器。根据各步骤的执行结果直接回答用户的目标。
规则：
1. 直接给出最终答案，不要复述步骤过程
2. 如果部分步骤失败，基于成功步骤的结果尽力回答，并简要说明局限
3. 用清晰自然的语言组织答案`

// Planner 生成/修订计划、综合作答
type Planner struct {
	chat ChatFunc
}

func NewPlanner(chat ChatFunc) *Planner {
	return &Planner{chat: chat}
}

// CreatePlan 为目标生成执行计划
func (p *Planner) CreatePlan(ctx context.Context, goal string) (*Plan, error) {
	userPrompt := fmt.Sprintf("用户目标：%s", goal)
	resp, err := p.chat(ctx, fmt.Sprintf(planSystemPrompt, MaxPlanSteps), userPrompt)
	if err != nil {
		return nil, fmt.Errorf("规划 LLM 调用失败: %w", err)
	}
	return ParsePlan(resp)
}

// Replan 基于已完成步骤与失败原因，重新规划剩余步骤
func (p *Planner) Replan(ctx context.Context, goal string, results []StepResult, failedStep PlanStep, failReason string) (*Plan, error) {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("原始目标：%s\n\n已完成/已尝试的步骤及结果：\n%s\n", goal, FormatResults(results)))
	sb.WriteString(fmt.Sprintf("失败步骤：%d - %s\n失败原因：%s\n\n", failedStep.ID, failedStep.Description, failReason))
	sb.WriteString("请输出修订后的剩余步骤计划。")

	resp, err := p.chat(ctx, replanSystemPrompt, sb.String())
	if err != nil {
		return nil, fmt.Errorf("重规划 LLM 调用失败: %w", err)
	}
	return ParsePlan(resp)
}

// Synthesize 综合各步骤结果生成最终答案
func (p *Planner) Synthesize(ctx context.Context, goal string, results []StepResult) (string, error) {
	resp, err := p.chat(ctx, synthesizeSystemPrompt, FormatResults(results))
	if err != nil {
		return "", fmt.Errorf("综合 LLM 调用失败: %w", err)
	}
	return strings.TrimSpace(resp), nil
}

// StepExecutor 单步执行器：把步骤交给带工具的 Agent 执行。
// priorResults 为前序步骤的结果摘要。
type StepExecutor func(ctx context.Context, step PlanStep, priorResults string) (string, error)

// ProgressListener 执行进度回调（用于流式推送步骤状态）
type ProgressListener func(event string)

// ExecutePlan 顺序执行计划；步骤失败时重规划一次（MaxReplanCount），
// 返回全部步骤结果（含失败步骤）。
func (p *Planner) ExecutePlan(ctx context.Context, plan *Plan, exec StepExecutor, onProgress ProgressListener) []StepResult {
	var allResults []StepResult
	replans := 0

	for {
		results := p.runSteps(ctx, plan, exec, onProgress)
		allResults = append(allResults, results...)

		failed := lastFailed(results)
		if failed == nil {
			return allResults
		}
		if replans >= MaxReplanCount {
			return allResults
		}

		failReason := failed.Output
		if failReason == "" {
			failReason = "步骤执行失败"
		}
		if onProgress != nil {
			onProgress(fmt.Sprintf("✖ 步骤 %d 失败，正在重新规划剩余步骤...\n", failed.Step.ID))
		}

		newPlan, err := p.Replan(ctx, plan.Goal, results, failed.Step, failReason)
		if err != nil {
			return allResults
		}
		replans++
		plan = newPlan
		if onProgress != nil {
			onProgress(newPlan.FormatPlan())
		}
	}
}

// runSteps 顺序执行一个计划的所有步骤，失败即停（重规划交给上层）
func (p *Planner) runSteps(ctx context.Context, plan *Plan, exec StepExecutor, onProgress ProgressListener) []StepResult {
	results := make([]StepResult, 0, len(plan.Steps))
	var prior strings.Builder

	total := len(plan.Steps)
	for i, step := range plan.Steps {
		if onProgress != nil {
			onProgress(fmt.Sprintf("▶ 执行步骤 %d/%d：%s\n", i+1, total, step.Description))
		}

		output, err := exec(ctx, step, prior.String())
		success := err == nil
		if !success && output == "" {
			output = err.Error()
		}

		results = append(results, StepResult{Step: step, Output: output, Success: success})

		if success {
			prior.WriteString(fmt.Sprintf("步骤%d【%s】结果：%s\n\n", step.ID, step.Description, truncateForContext(output)))
			if onProgress != nil {
				onProgress(fmt.Sprintf("✔ 步骤 %d/%d 完成\n", i+1, total))
			}
		} else {
			if onProgress != nil {
				onProgress(fmt.Sprintf("✖ 步骤 %d/%d 失败：%s\n", i+1, total, truncateForContext(err.Error())))
			}
			return results
		}
	}
	return results
}

// BuildStepPrompt 构建单步执行的 prompt（纯函数，可单测）：
// 步骤描述 + 建议工具 + 前序结果参考。
func BuildStepPrompt(step PlanStep, priorResults string) string {
	var sb strings.Builder
	sb.WriteString("请完成以下任务步骤：")
	sb.WriteString(step.Description)
	if step.Tool != "" {
		sb.WriteString(fmt.Sprintf("\n建议优先使用工具：%s", step.Tool))
	}
	if priorResults != "" {
		sb.WriteString("\n\n前序步骤的结果（供参考）：\n")
		sb.WriteString(priorResults)
	}
	return sb.String()
}

// lastFailed 返回最后一个失败结果（无则 nil）
func lastFailed(results []StepResult) *StepResult {
	for i := len(results) - 1; i >= 0; i-- {
		if !results[i].Success {
			return &results[i]
		}
	}
	return nil
}

func truncateForContext(s string) string {
	if len(s) > maxStepOutputKB*1024 {
		return s[:maxStepOutputKB*1024] + "...(截断)"
	}
	return s
}
