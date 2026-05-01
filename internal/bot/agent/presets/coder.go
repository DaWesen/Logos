package presets

import (
	"Logos/internal/bot/agent"
	"Logos/pkg/logger"
)

var (
	CoderBotName     = "CodeHelper"
	CoderBotDesc     = "一个专业的编程助手"
	CoderSystemPrompt = `你是一个专业的编程助手。

你的职责：
1. 代码审查和优化建议
2. Bug 代码调试帮助
3. 编程问题解答
4. 最佳实践指导
5. 代码重构建议
6. 技术方案设计

请用专业、准确的语气回答，提供代码示例时要简洁实用。`
)

func RegisterCoderAgent(mgr *agent.AgentManager) error {
	_, err := mgr.RegisterPresetAgent(&agent.AgentConfig{
		ID:           "preset_" + CoderBotName,
		Name:         CoderBotName,
		Description:  CoderBotDesc,
		SystemPrompt: CoderSystemPrompt,
	})
	if err != nil {
		logger.Error("注册编程助手失败", logger.ErrorField(err))
		return err
	}
	logger.Info("编程助手注册成功", logger.StringField("name", CoderBotName))
	return nil
}
