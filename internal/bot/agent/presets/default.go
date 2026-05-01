package presets

import (
	"Logos/internal/bot/agent"
	"Logos/pkg/logger"
)

var (
	DefaultBotName      = "Assistant"
	DefaultBotDesc      = "A helpful AI assistant"
	DefaultSystemPrompt = `你是一个乐于助人的AI助手。
你的职责是帮助用户解答问题、提供建议、完成任务。
你应该友好、专业、准确地回答。`
)

func RegisterDefaultAgent(mgr *agent.AgentManager) error {
	_, err := mgr.RegisterPresetAgent(&agent.AgentConfig{
		ID:           "preset_" + DefaultBotName,
		Name:         DefaultBotName,
		Description:  DefaultBotDesc,
		SystemPrompt: DefaultSystemPrompt,
	})
	if err != nil {
		logger.Error("注册默认助手失败", logger.ErrorField(err))
		return err
	}
	logger.Info("默认助手注册成功", logger.StringField("name", DefaultBotName))
	return nil
}
