package planner

import (
	"context"
	"fmt"
	"strings"
)

// ChatFunc 规划器专用的 LLM 调用（注入式，便于测试）
type ChatFunc func(ctx context.Context, systemPrompt, userPrompt string) (string, error)

const planSystemPrompt = `你是任务规划器。把用户目标分解为带依赖关系的步骤图。
规则：
1. 步骤数在 1 到 %d 之间，每步是一个原子子任务，能用现有工具（知识库检索、图谱查询、MCP 工具等）或模型知识完成
2. 如果目标足够简单一步可完成，就只输出一个步骤
3. 每步的 description 要具体、自包含（执行者看不到其他上下文）
4. tool 字段填建议使用的工具名，没有合适工具就留空
5. depends_on 填直接前驱步骤的 id：能并行的步骤不要串成一条链
6. assumptions 写这一步成立所依赖的前提（一条一句）；没有就留空数组
7. expects 用一句话写这一步应产出什么，用于判断"真的完成了"而不只是"没报错"
8. effect 只能是 none / idempotent / side_effect：只读检索用 none，可重复写入用 idempotent，
   发消息、发邮件、扣款等不可重放动作必须标记 side_effect
输出 JSON，不要输出其他内容：
{"goal": "目标复述", "steps": [{"id": 1, "description": "步骤描述", "tool": "工具名或空",
  "depends_on": [], "assumptions": [], "expects": "预期产出", "effect": "none"}]}`

const replanSystemPrompt = `你是任务规划器。之前的计划在执行中失败，需要修订。
规则：
1. 只输出尚未完成的剩余步骤（重新从 id 1 计数），已完成的步骤结果会提供给你参考
2. 针对失败原因调整方案：换工具、换角度，或拆分得更细
3. 输出 JSON，格式与原计划相同`

const synthesizeSystemPrompt = `你是任务综合器。根据各步骤的执行结果直接回答用户的目标。
规则：
1. 直接给出最终答案，不要复述步骤过程
2. 如果部分步骤失败，基于成功步骤的结果尽力回答，并简要说明局限
3. 标记为【已失效】的步骤结果一律不得引用，也不要猜测其内容
4. 用清晰自然的语言组织答案`

// Planner 生成/修订计划、综合作答
type Planner struct {
	chat ChatFunc
}

func NewPlanner(chat ChatFunc) *Planner {
	return &Planner{chat: chat}
}

// CreatePlan 为目标生成执行计划
func (p *Planner) CreatePlan(ctx context.Context, goal string) (*Plan, error) {
	return p.CreatePlanWithBudget(ctx, goal, nil)
}

// CreatePlanWithBudget 与 CreatePlan 相同，但把这一次调用计入共享预算。
//
// 规划、执行、综合必须是同一本账：只要有一条路径自己 new 一个 Budget，
// 总调用数上限就可以被绕过，预算就只剩"每一步看起来都不多"。
func (p *Planner) CreatePlanWithBudget(ctx context.Context, goal string, budget *Budget) (*Plan, error) {
	userPrompt := fmt.Sprintf("用户目标：%s", goal)
	resp, err := p.chatWithBudget(ctx, fmt.Sprintf(planSystemPrompt, MaxPlanSteps), userPrompt, budget)
	if err != nil {
		return nil, fmt.Errorf("规划 LLM 调用失败: %w", err)
	}
	return ParsePlan(resp)
}

// Replan 基于已完成步骤与失败原因，重新规划剩余步骤
func (p *Planner) Replan(ctx context.Context, goal string, results []StepResult, failedStep PlanStep, failReason string) (*Plan, error) {
	return p.replanWithBudget(ctx, goal, results, failedStep, failReason, nil)
}

func (p *Planner) replanWithBudget(ctx context.Context, goal string, results []StepResult,
	failedStep PlanStep, failReason string, budget *Budget) (*Plan, error) {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("原始目标：%s\n\n已完成/已尝试的步骤及结果：\n%s\n", goal, FormatResults(results)))
	sb.WriteString(fmt.Sprintf("失败步骤：%d - %s\n失败原因：%s\n\n", failedStep.ID, failedStep.Description, failReason))
	sb.WriteString("请输出修订后的剩余步骤计划。")

	resp, err := p.chatWithBudget(ctx, replanSystemPrompt, sb.String(), budget)
	if err != nil {
		return nil, fmt.Errorf("重规划 LLM 调用失败: %w", err)
	}
	return ParsePlan(resp)
}

// Synthesize 综合各步骤结果生成最终答案
func (p *Planner) Synthesize(ctx context.Context, goal string, results []StepResult) (string, error) {
	return p.SynthesizeWithBudget(ctx, goal, results, nil)
}

// SynthesizeWithBudget 与 Synthesize 相同，但把这一次调用计入共享预算。
func (p *Planner) SynthesizeWithBudget(ctx context.Context, goal string, results []StepResult, budget *Budget) (string, error) {
	// 综合 prompt 必须显式带上目标：只给步骤结果时，模型可能答成
	// "这些步骤做了什么"，而不是"用户要的答案是什么"。
	userPrompt := fmt.Sprintf("用户目标：%s\n\n各步骤执行结果：\n%s", goal, FormatSynthesisResults(results))
	resp, err := p.chatWithBudget(ctx, synthesizeSystemPrompt, userPrompt, budget)
	if err != nil {
		return "", fmt.Errorf("综合 LLM 调用失败: %w", err)
	}
	return strings.TrimSpace(resp), nil
}

// DegradedSummary 在综合阶段不可用时，把已经成功的结果直接交给用户。
//
// 此时再回退普通对话会重新规划、重新调用工具，既烧一遍预算，又可能
// 把已经完成的副作用再做一次。部分结果不完美，但比"全部丢掉"更诚实。
func DegradedSummary(results []StepResult) string {
	done := make([]StepResult, 0, len(results))
	for _, r := range results {
		if r.Success && !r.Stale {
			done = append(done, r)
		}
	}
	if len(done) == 0 {
		return ""
	}
	return "已完成的部分结果（综合阶段受限）：\n" + FormatResults(done)
}

// chatWithBudget 是所有规划器 LLM 调用的唯一入口。
//
// 先用 prompt 估算预留输入 token，拿到响应后再补响应 token；
// 估算比例按 UTF-8 字节/3 计算，中文约 1 token/字，英文略偏保守。
func (p *Planner) chatWithBudget(ctx context.Context, systemPrompt, userPrompt string, budget *Budget) (string, error) {
	if budget != nil {
		if !budget.TryReserve(1) {
			return "", fmt.Errorf("%w: 调用次数已用尽", ErrBudgetExhausted)
		}
		if !budget.TryReserveTokens(EstimateTokens(systemPrompt) + EstimateTokens(userPrompt)) {
			// token 预留失败时这次调用不会发生，把调用额度退回去，
			// 否则连续几次 token 超限就会在调用次数上留下"幽灵消耗"。
			budget.Release(1)
			return "", fmt.Errorf("%w: token 预算已用尽", ErrBudgetExhausted)
		}
	}
	resp, err := p.chat(ctx, systemPrompt, userPrompt)
	if budget != nil && err == nil {
		budget.AddTokens(EstimateTokens(resp))
	}
	return resp, err
}

// EstimateTokens 在供应商未回传 usage 时估算 token 数。
// 按字节/3 估算：中文 3 字节≈1 token，英文 4 字节≈1 token，取偏保守的中间值。
func EstimateTokens(s string) int {
	if s == "" {
		return 0
	}
	return (len(s) + 2) / 3
}

// StepExecutor 单步执行器：把步骤交给带工具的 Agent 执行。
// priorResults 为前序步骤的结果摘要。
type StepExecutor func(ctx context.Context, step PlanStep, priorResults string) (string, error)

// ProgressListener 执行进度回调（用于流式推送步骤状态）
type ProgressListener func(event string)

// ExecutePlan 顺序执行计划；步骤失败时重规划一次（MaxReplanCount），
// 返回全部步骤结果（含失败步骤）。
func (p *Planner) ExecutePlan(ctx context.Context, plan *Plan, exec StepExecutor, onProgress ProgressListener) []StepResult {
	return p.ExecutePlanWithBudget(ctx, plan, exec, onProgress, nil)
}

// ExecutePlanWithBudget 是顺序执行器的预算版本。
// legacy 路径保留原签名，但服务层会传入同一轮共享预算，避免两条路径记账口径不同。
func (p *Planner) ExecutePlanWithBudget(ctx context.Context, plan *Plan, exec StepExecutor,
	onProgress ProgressListener, budget *Budget) []StepResult {
	var allResults []StepResult
	replans := 0

	for {
		results := p.runSteps(ctx, plan, exec, onProgress, budget)
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

		// D7：把历史轮次结果一并交给重规划，避免第二轮看不到第一轮已完成的步骤。
		// D6：目标不可被模型改写，重规划只允许调整步骤。
		newPlan, err := p.replanWithBudget(ctx, plan.Goal, allResults, failed.Step, failReason, budget)
		if err != nil {
			return allResults
		}
		newPlan.Goal = plan.Goal
		replans++
		plan = newPlan
		if onProgress != nil {
			onProgress(newPlan.FormatPlan())
		}
	}
}

// runSteps 顺序执行一个计划的所有步骤，失败即停（重规划交给上层）
func (p *Planner) runSteps(ctx context.Context, plan *Plan, exec StepExecutor, onProgress ProgressListener, budget *Budget) []StepResult {
	results := make([]StepResult, 0, len(plan.Steps))
	var prior strings.Builder

	total := len(plan.Steps)
	for i, step := range plan.Steps {
		if budget != nil && !budget.TryReserve(1) {
			results = append(results, StepResult{
				Step: step, Output: ErrBudgetExhausted.Error(), Success: false, Err: ErrBudgetExhausted,
			})
			return results
		}
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
// 步骤描述 + 建议工具 + 成立前提 + 预期产出 + 前序结果参考。
//
// Assumptions/Expects 不参与调度，但必须进入执行 prompt：模型只有看到
// 「这一步假设了什么、要交付什么」，才会在前提不成立时上报，而不是硬编
// 一个看起来完成的答案。校验器是事后兜底，不能替代执行时的上下文。
func BuildStepPrompt(step PlanStep, priorResults string) string {
	var sb strings.Builder
	sb.WriteString("请完成以下任务步骤：\n")
	sb.WriteString(step.Description)
	if step.Tool != "" {
		sb.WriteString(fmt.Sprintf("\n建议优先使用工具：%s", step.Tool))
	}
	if len(step.Assumptions) > 0 {
		sb.WriteString("\n\n本步骤成立的前提（不成立时请在正文说明并写入 violations）：")
		for _, assumption := range step.Assumptions {
			sb.WriteString("\n- ")
			sb.WriteString(assumption)
		}
	}
	if strings.TrimSpace(step.Expects) != "" {
		sb.WriteString("\n\n预期产出：")
		sb.WriteString(step.Expects)
	}
	if priorResults != "" {
		sb.WriteString("\n\n前序步骤的结果（供参考）：\n")
		sb.WriteString(priorResults)
	}
	sb.WriteString(`

【结构化输出】
完成上述步骤后，在回答末尾追加一行 JSON（不要用代码块包裹）：
{"facts":[{"key":"2.doc_ids","value":"d_1,d_2"}],
 "violations":[{"step_id":2,"claim":"文件是 CSV","reason":"实际是 XLSX"}]}
没有可上报的内容时输出 {"facts":[],"violations":[]}。
如果发现前序步骤的结论不成立，必须写进 violations，不要只在正文里提及。`)
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
	return truncateBytes(s, maxStepOutputBytes)
}
