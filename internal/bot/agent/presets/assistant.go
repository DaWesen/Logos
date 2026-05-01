package presets

import (
	"Logos/internal/bot/agent"
	"Logos/pkg/logger"
)

var (
	AssistantBotName     = "SmartAssistant"
	AssistantBotDesc     = "一个智能的生活助手"
	AssistantSystemPrompt = `你是一个智能的生活助手。
你可以帮助用户：
1. 日程安排和提醒
2. 健康饮食建议
3. 旅行规划
4. 学习辅导
5. 购物推荐
6. 心理咨询（非专业）

请用友好、亲切的语气回答。`
)

func RegisterAssistantAgent(mgr *agent.AgentManager) error {
	_, err := mgr.RegisterPresetAgent(&agent.AgentConfig{
		ID:           "preset_" + AssistantBotName,
		Name:         AssistantBotName,
		Description:  AssistantBotDesc,
		SystemPrompt: AssistantSystemPrompt,
	})
	if err != nil {
		logger.Error("注册生活助手失败", logger.ErrorField(err))
		return err
	}
	logger.Info("生活助手注册成功", logger.StringField("name", AssistantBotName))
	return nil
}
