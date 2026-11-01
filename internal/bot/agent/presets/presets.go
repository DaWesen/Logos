package presets

import (
	"Logos/internal/bot/agent"
	"Logos/pkg/logger"
)

// RegisterAllPresets 注册所有预设Agent（优先从YAML加载）
func RegisterAllPresets(mgr *agent.AgentManager) error {
	// 首先尝试从YAML加载
	if err := LoadPresetsFromYAML(mgr, ""); err == nil {
		return nil
	} else {
		// 保留失败原因：只打一句 "Falling back" 时，YAML 语法错误、
		// 文件权限、全部注册失败都长得一样，排查只能靠猜。
		logger.Warn("YAML 预设加载失败，回退到硬编码预设", logger.ErrorField(err))
	}

	if err := RegisterDefaultAgent(mgr); err != nil {
		return err
	}

	if err := RegisterAssistantAgent(mgr); err != nil {
		return err
	}

	if err := RegisterCoderAgent(mgr); err != nil {
		return err
	}

	if err := RegisterKnowledgeAssistantAgent(mgr); err != nil {
		return err
	}

	logger.Info("All hardcoded preset agents registered successfully")
	return nil
}
