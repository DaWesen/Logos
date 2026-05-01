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
	}

	// YAML加载失败，回退到硬编码
	logger.Warn("Falling back to hardcoded presets")

	if err := RegisterDefaultAgent(mgr); err != nil {
		return err
	}

	if err := RegisterAssistantAgent(mgr); err != nil {
		return err
	}

	if err := RegisterCoderAgent(mgr); err != nil {
		return err
	}

	logger.Info("All hardcoded preset agents registered successfully")
	return nil
}
