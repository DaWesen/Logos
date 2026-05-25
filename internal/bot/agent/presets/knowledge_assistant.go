package presets

import (
	"Logos/internal/bot/agent"
	"Logos/pkg/logger"
)

// RegisterKnowledgeAssistantAgent 注册知识库助手Agent
func RegisterKnowledgeAssistantAgent(mgr *agent.AgentManager) error {
	logger.Info("正在注册知识库助手Agent")

	cfg := &agent.AgentConfig{
		ID:          "knowledge_assistant",
		Name:        "知识库助手",
		Description: "基于知识库的智能问答助手，可以检索知识库内容并回答用户的专业问题。",
		SystemPrompt: `你是一个专业的知识库助手，擅长从知识库中检索信息来回答用户的问题。

工作方式：
1. 理解用户的问题
2. 从知识库中检索相关的信息
3. 基于检索结果给出准确、专业的回答
4. 如果知识库中没有相关内容，坦诚告知用户

回答原则：
- 优先使用知识库中的信息
- 引用知识库内容时要标明来源
- 保持回答专业、准确
- 不知道就说不知道，不要编造信息`,
	}

	_, err := mgr.GetOrCreateAgent(cfg)
	if err != nil {
		logger.Error("注册知识库助手Agent失败", logger.ErrorField(err))
		return err
	}

	logger.Info("知识库助手Agent注册成功")
	return nil
}
