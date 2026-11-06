package planner

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestParsePlan_ValidJSON(t *testing.T) {
	raw := `{"goal": "分析项目文档并总结", "steps": [
		{"id": 1, "description": "检索知识库中的项目文档", "tool": "knowledge_search"},
		{"id": 2, "description": "总结检索到的内容"}
	]}`
	plan, err := ParsePlan(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.Goal != "分析项目文档并总结" || len(plan.Steps) != 2 {
		t.Fatalf("plan parsed wrong: %+v", plan)
	}
	if plan.Steps[0].Tool != "knowledge_search" {
		t.Errorf("step tool = %q", plan.Steps[0].Tool)
	}
}

func TestParsePlan_ToleratesMarkdownFence(t *testing.T) {
	raw := "```json\n{\"goal\": \"g\", \"steps\": [{\"id\": 1, \"description\": \"s\"}]}\n```"
	if _, err := ParsePlan(raw); err != nil {
		t.Fatalf("markdown-wrapped JSON should parse: %v", err)
	}
}

func TestParsePlan_TrimExtraText(t *testing.T) {
	raw := "好的，以下是计划：\n{\"goal\": \"g\", \"steps\": [{\"id\": 1, \"description\": \"s\"}]}\n希望对你有帮助"
	if _, err := ParsePlan(raw); err != nil {
		t.Fatalf("surrounded JSON should parse: %v", err)
	}
}

func TestValidatePlan(t *testing.T) {
	tests := []struct {
		name  string
		plan  *Plan
		valid bool
	}{
		{"正常计划", &Plan{Goal: "g", Steps: []PlanStep{{ID: 1, Description: "a"}}}, true},
		{"无目标", &Plan{Steps: []PlanStep{{ID: 1, Description: "a"}}}, false},
		{"无步骤", &Plan{Goal: "g"}, false},
		{"步骤超上限", &Plan{Goal: "g", Steps: makeSteps(MaxPlanSteps + 1)}, false},
		{"空描述", &Plan{Goal: "g", Steps: []PlanStep{{ID: 1}}}, false},
		{"id 重复", &Plan{Goal: "g", Steps: []PlanStep{{ID: 1, Description: "a"}, {ID: 1, Description: "b"}}}, false},
		{"id 非法", &Plan{Goal: "g", Steps: []PlanStep{{ID: 0, Description: "a"}}}, false},
		{"nil", nil, false},
	}
	for _, tt := range tests {
		err := ValidatePlan(tt.plan)
		if (err == nil) != tt.valid {
			t.Errorf("%s: valid=%v got err=%v", tt.name, tt.valid, err)
		}
	}
}

// TestValidatePlan_DependencyGraph 覆盖 T2/T3/T4。
// 依赖图校验必须在执行前失败：等到调度阶段才发现环，计划已经占用了
// 规划调用与运行记录，回滚成本远高于入口处一次纯函数校验。
func TestValidatePlan_DependencyGraph(t *testing.T) {
	tests := []struct {
		name    string
		steps   []PlanStep
		wantErr []string
	}{
		{
			name:    "自环",
			steps:   []PlanStep{{ID: 1, Description: "a", DependsOn: []int{1}}},
			wantErr: []string{"依赖自身"},
		},
		{
			name: "两步环",
			steps: []PlanStep{
				{ID: 1, Description: "a", DependsOn: []int{2}},
				{ID: 2, Description: "b", DependsOn: []int{1}},
			},
			wantErr: []string{"依赖环", "1 -> 2 -> 1"},
		},
		{
			name: "依赖不存在",
			steps: []PlanStep{
				{ID: 1, Description: "a"},
				{ID: 2, Description: "b", DependsOn: []int{9}},
			},
			wantErr: []string{"依赖不存在", "9"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePlan(&Plan{Goal: "g", Steps: tt.steps})
			if err == nil {
				t.Fatal("expected validation error")
			}
			for _, want := range tt.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q should contain %q", err, want)
				}
			}
		})
	}
}

func makeSteps(n int) []PlanStep {
	steps := make([]PlanStep, n)
	for i := range steps {
		steps[i] = PlanStep{ID: i + 1, Description: fmt.Sprintf("步骤%d", i+1)}
	}
	return steps
}

// fakeChat 按调用顺序返回预设响应的假 LLM，并记录收到的 prompt
type fakeChat struct {
	responses []string
	prompts   []string
	calls     int
}

func (f *fakeChat) call(ctx context.Context, system, user string) (string, error) {
	f.prompts = append(f.prompts, system+"\n---\n"+user)
	if f.calls >= len(f.responses) {
		return "", fmt.Errorf("no more fake responses")
	}
	resp := f.responses[f.calls]
	f.calls++
	return resp, nil
}

func validPlanJSON(n int) string {
	return fmt.Sprintf(`{"goal": "目标", "steps": %s}`, stepsJSON(makeSteps(n)))
}

func stepsJSON(steps []PlanStep) string {
	parts := make([]string, len(steps))
	for i, s := range steps {
		parts[i] = fmt.Sprintf(`{"id": %d, "description": "%s"}`, s.ID, s.Description)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func TestPlanner_CreatePlan(t *testing.T) {
	chat := &fakeChat{responses: []string{validPlanJSON(3)}}
	p := NewPlanner(chat.call)

	plan, err := p.CreatePlan(t.Context(), "复杂任务")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.Steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(plan.Steps))
	}
	// 规划 prompt 应包含目标与步骤数约束
	if len(chat.prompts) != 1 || !strings.Contains(chat.prompts[0], "复杂任务") {
		t.Errorf("plan prompt should contain the goal, got %v", chat.prompts)
	}
	if !strings.Contains(chat.prompts[0], "任务规划器") {
		t.Errorf("plan prompt should use planner system prompt")
	}
}

func TestPlanner_ExecutePlan_AllStepsSucceed(t *testing.T) {
	chat := &fakeChat{responses: []string{validPlanJSON(2)}}
	p := NewPlanner(chat.call)

	var executed []string
	var progress []string
	results := p.ExecutePlan(t.Context(), mustPlan(t, 2), func(ctx context.Context, step PlanStep, prior string) (string, error) {
		executed = append(executed, step.Description)
		return fmt.Sprintf("%s的结果", step.Description), nil
	}, func(event string) { progress = append(progress, event) })

	if len(results) != 2 || !results[0].Success || !results[1].Success {
		t.Fatalf("expected 2 successful steps, got %+v", results)
	}
	if len(executed) != 2 {
		t.Fatalf("executor should run both steps, ran %d", len(executed))
	}
	// 进度事件包含步骤状态（计划展示由调用方负责）
	joined := strings.Join(progress, "\n")
	for _, want := range []string{"▶ 执行步骤 1/2", "✔ 步骤 1/2 完成", "✔ 步骤 2/2 完成"} {
		if !strings.Contains(joined, want) {
			t.Errorf("progress missing %q: %v", want, progress)
		}
	}
}

func TestPlanner_ExecutePlan_PassesPriorResultsToNextStep(t *testing.T) {
	p := NewPlanner(func(ctx context.Context, s, u string) (string, error) { return "", fmt.Errorf("unused") })

	var priorSeen []string
	_ = p.ExecutePlan(t.Context(), mustPlan(t, 2), func(ctx context.Context, step PlanStep, prior string) (string, error) {
		priorSeen = append(priorSeen, prior)
		return fmt.Sprintf("step-%d-out", step.ID), nil
	}, nil)

	// 第二步应看到第一步的结果
	if len(priorSeen) != 2 {
		t.Fatalf("expected 2 executions, got %d", len(priorSeen))
	}
	if priorSeen[0] != "" {
		t.Errorf("first step should have no prior, got %q", priorSeen[0])
	}
	if !strings.Contains(priorSeen[1], "step-1-out") {
		t.Errorf("second step should see first result, got %q", priorSeen[1])
	}
}

func TestPlanner_ExecutePlan_FailureTriggersReplan(t *testing.T) {
	// 初始计划直接传入（不经 CreatePlan），Replan 使用第一个 LLM 响应：
	// 一个"替代方案"单步计划
	replanJSON := `{"goal": "目标", "steps": [{"id": 1, "description": "替代方案"}]}`
	chat := &fakeChat{responses: []string{replanJSON}}
	p := NewPlanner(chat.call)

	var calls int
	results := p.ExecutePlan(t.Context(), mustPlan(t, 2), func(ctx context.Context, step PlanStep, prior string) (string, error) {
		calls++
		if step.ID == 2 && calls <= 2 {
			return "", fmt.Errorf("工具不可用")
		}
		return fmt.Sprintf("ok-%d", step.ID), nil
	}, nil)

	// 原计划步骤1成功、步骤2失败 → replan → 替代步骤成功
	var succeeded []string
	for _, r := range results {
		if r.Success {
			succeeded = append(succeeded, r.Step.Description)
		}
	}
	if len(succeeded) != 2 || succeeded[1] != "替代方案" {
		t.Fatalf("expected step1 + replanned step to succeed, got %+v", results)
	}
	// LLM 调用次数：仅 Replan 一次
	if chat.calls != 1 {
		t.Errorf("LLM calls = %d, want 1 (replan only)", chat.calls)
	}
}

func TestPlanner_ExecutePlan_ReplanOnlyOnce(t *testing.T) {
	// 失败 → replan → 再失败 → 不再 replan（MaxReplanCount=1）
	replanJSON := `{"goal": "目标", "steps": [{"id": 1, "description": "重试"}]}`
	chat := &fakeChat{responses: []string{validPlanJSON(1), replanJSON}}
	p := NewPlanner(chat.call)

	_ = p.ExecutePlan(t.Context(), mustPlan(t, 1), func(ctx context.Context, step PlanStep, prior string) (string, error) {
		return "", fmt.Errorf("总是失败")
	}, nil)

	if chat.calls > 2 {
		t.Errorf("replan must be capped at 1, LLM calls = %d", chat.calls)
	}
}

func TestBuildStepPrompt(t *testing.T) {
	step := PlanStep{ID: 1, Description: "检索文档", Tool: "knowledge_search"}
	got := BuildStepPrompt(step, "")
	if !strings.Contains(got, "检索文档") || !strings.Contains(got, "knowledge_search") {
		t.Errorf("step prompt missing parts: %q", got)
	}

	got = BuildStepPrompt(step, "前序结果ABC")
	if !strings.Contains(got, "前序结果ABC") {
		t.Errorf("step prompt should carry prior results: %q", got)
	}

	// 前提与预期必须进入执行 prompt：只在执行后校验，模型已经按
	// 错误前提编完了答案。
	step.Assumptions = []string{"知识库中存在《架构设计》"}
	step.Expects = "列出三条关键约束"
	got = BuildStepPrompt(step, "")
	for _, want := range []string{"知识库中存在《架构设计》", "列出三条关键约束"} {
		if !strings.Contains(got, want) {
			t.Errorf("step prompt missing %q: %q", want, got)
		}
	}
}

func TestFormatResults(t *testing.T) {
	results := []StepResult{
		{Step: PlanStep{ID: 1, Description: "第一步"}, Output: "结果一", Success: true},
		{Step: PlanStep{ID: 2, Description: "第二步"}, Output: "失败原因", Success: false},
	}
	got := FormatResults(results)
	for _, want := range []string{"第一步", "结果一", "成功", "第二步", "失败", "失败原因"} {
		if !strings.Contains(got, want) {
			t.Errorf("FormatResults missing %q: %s", want, got)
		}
	}
}

// TestTruncateRunes_ChineseNotCut 上限按字符计：中文不能被切出半个字。
func TestTruncateRunes_ChineseNotCut(t *testing.T) {
	if got := truncateRunes("你好世界", 3); got != "你好世...(截断)" {
		t.Fatalf("got %q", got)
	}
	if got := truncateRunes("短文本", 5); got != "短文本" {
		t.Fatalf("未超限不应改动，got %q", got)
	}
	if got := truncateRunes("", 0); got != "" {
		t.Fatalf("maxRunes<=0 应返回空串，got %q", got)
	}
}

// TestFormatResults_TruncatesByRune 单步结果截断后必须是合法 UTF-8（会作为 prompt 发给模型）。
func TestFormatResults_TruncatesByRune(t *testing.T) {
	long := strings.Repeat("中文结果", maxStepOutputBytes/4+1)
	got := FormatResults([]StepResult{
		{Step: PlanStep{ID: 1, Description: "第一步"}, Output: long, Success: true},
	})

	if !utf8.ValidString(got) {
		t.Fatal("截断后产生了非法 UTF-8")
	}
	if !strings.Contains(got, "...(截断)") {
		t.Fatalf("超限结果缺少截断标记，长度=%d", len(got))
	}
}

// TestTruncateBytes_MultibyteBoundary 覆盖 T16：max 正好落在 UTF-8
// 序列中间时，必须回退到字符起始位置。半个汉字既会让部分模型直接 400，
// 也会在流式输出里变成乱码。
func TestTruncateBytes_MultibyteBoundary(t *testing.T) {
	const marker = "...(截断)"
	text := "中文结果测试" // 每个汉字 3 字节，max 很容易落在序列中间

	for max := 1; max <= len(text); max++ {
		got := truncateBytes(text, max)
		if !utf8.ValidString(got) {
			t.Fatalf("max=%d 产生了非法 UTF-8: %q", max, got)
		}
		if len(got) > max+len(marker) {
			t.Fatalf("max=%d 截断后长度 %d 超过上限 %d", max, len(got), max+len(marker))
		}
	}

	if got := truncateBytes("你好世界", 4); got != "你"+marker {
		t.Fatalf("max 落在第二个字中间时应回退到首字边界，got %q", got)
	}
}

// TestFormatResults_TruncatesByBytes 覆盖 T17：上限是 16KB 字节而不是
// 16K 字符。中文按字节计会被更早截断，正文长度应落在上限附近而非翻三倍。
func TestFormatResults_TruncatesByBytes(t *testing.T) {
	const marker = "...(截断)"
	long := strings.Repeat("中", 20*1024/3) // 约 20KB，单字 3 字节
	if len(long) <= maxStepOutputBytes {
		t.Fatalf("测试输入应超过 %d 字节，got %d", maxStepOutputBytes, len(long))
	}

	got := FormatResults([]StepResult{
		{Step: PlanStep{ID: 1, Description: "第一步"}, Output: long, Success: true},
	})
	if !utf8.ValidString(got) {
		t.Fatal("按字节截断后产生了非法 UTF-8")
	}

	const prefix = "步骤 1：第一步【成功】\n结果："
	if !strings.HasPrefix(got, prefix) {
		t.Fatalf("结果前綴不符：%q", got)
	}
	body := strings.TrimSuffix(strings.TrimPrefix(got, prefix), "\n\n")
	if !strings.HasSuffix(body, marker) {
		t.Fatalf("超限结果缺少截断标记，正文长度=%d", len(body))
	}
	if len(body) > maxStepOutputBytes+len(marker) {
		t.Errorf("正文 %d 字节超过上限 %d", len(body), maxStepOutputBytes+len(marker))
	}
	// 回退最多跨过一个 3 字节字符，因此保留正文与上限的误差应小于 4 字节。
	if len(body) < maxStepOutputBytes-4+len(marker) {
		t.Errorf("正文只保留了 %d 字节，离上限 %d 太远", len(body), maxStepOutputBytes)
	}
}

func mustPlan(t *testing.T, n int) *Plan {
	t.Helper()
	plan := &Plan{Goal: "目标", Steps: makeSteps(n)}
	if err := ValidatePlan(plan); err != nil {
		t.Fatalf("invalid test plan: %v", err)
	}
	return plan
}
