package service

import (
	"context"
	"encoding/json"
	"fmt"

	botmodel "Logos/internal/service/ai/bot/model"
	"Logos/pkg/logger"
	"Logos/pkg/strutil"
)

// ==================== Agent 自我反思（Self-Critique） ====================
//
// 答案输出前由独立的审查 LLM 把关：
//   - 是否真正回答了用户的问题（不跑偏）
//   - 是否有无依据的断言（幻觉）
//   - 是否遗漏关键要求
// 审查不通过时用修订版替换输出；审查本身失败则原答案直出（不因质检阻塞主流程）。
//
// 成本权衡：开启后每次对话多一次 LLM 调用，因此做成 Bot 级开关
// （enable_reflection）；流式路径不反思（答案已逐 token 给出，
// 事后替换会造成"答案跳变"），建议配合非流式接口使用。

// reflectionEnabled 判断 Bot 是否开启自我反思
func reflectionEnabled(bot *botmodel.Bot) bool {
	return bot != nil && bot.Config["enable_reflection"] == "true"
}

const reflectionSystemPrompt = `你是答案审查器。根据用户的问题与候选答案进行审查，只输出 JSON。
审查清单：
1. 相关性：答案是否正面回答了问题
2. 依据性：答案中的关键断言是否有支撑（工具结果/知识库内容/问题本身），有无编造
3. 完整性：问题中的关键要求是否被遗漏
输出格式：
{"pass": true/false, "issues": "问题摘要（pass 时留空）", "revised": "不通过时的修订答案（pass 时留空）"}
注意：revised 必须是完整可用的最终答案，不是修改说明。只输出 JSON。`

type reflectionResult struct {
	Pass    bool   `json:"pass"`
	Issues  string `json:"issues"`
	Revised string `json:"revised"`
}

// parseReflection 解析审查结果（纯函数，可单测）
func parseReflection(raw string) (*reflectionResult, error) {
	cleaned := strutil.ExtractJSON(raw)
	var r reflectionResult
	if err := json.Unmarshal([]byte(cleaned), &r); err != nil {
		return nil, fmt.Errorf("审查结果解析失败: %w", err)
	}
	return &r, nil
}

// reflectAnswer 对答案做质量审查，返回（最终答案, 是否被修订）。
// 审查 LLM 失败或结果异常时原样返回（质检是增益，不是闸门）。
func (s *botServiceImpl) reflectAnswer(ctx context.Context, question, answer string) (string, bool) {
	userPrompt := fmt.Sprintf("用户的问题：\n%s\n\n候选答案：\n%s", question, answer)

	resp, err := s.plannerChat(ctx, reflectionSystemPrompt, userPrompt)
	if err != nil {
		logger.Warn("自我反思 LLM 调用失败，答案原样输出", logger.ErrorField(err))
		return answer, false
	}

	result, err := parseReflection(resp)
	if err != nil {
		logger.Warn("自我反思结果解析失败，答案原样输出", logger.ErrorField(err))
		return answer, false
	}

	if result.Pass {
		return answer, false
	}

	if result.Revised == "" {
		logger.Warn("审查未通过但无修订版，答案原样输出",
			logger.StringField("issues", result.Issues))
		return answer, false
	}

	logger.Info("自我反思已修订答案",
		logger.StringField("issues", result.Issues),
		logger.IntField("orig_len", len(answer)),
		logger.IntField("revised_len", len(result.Revised)))
	return result.Revised, true
}
