package bot

import (
	"Logos/internal/bot/agent"
	"Logos/internal/bot/agent/presets"
	"Logos/pkg/eino"
	"Logos/pkg/logger"
)

// Bot 模块管理器
type Manager struct {
	agentManager *agent.AgentManager
}

var (
	globalManager *Manager
)

// Init 初始化 Bot 模块
func Init() error {
	logger.Info("Initializing Bot module...")

	// 初始化 Eino
	einoManager, err := eino.InitEino()
	if err != nil {
		logger.Error("Failed to init Eino", logger.ErrorField(err))
		return err
	}

	// 初始化 Agent 管理器
	am := agent.NewAgentManager(einoManager)
	agent.InitAgentManager(einoManager)

	// 注册所有预设
	if err := presets.RegisterAllPresets(am); err != nil {
		logger.Warn("Failed to register presets, but continue", logger.ErrorField(err))
	} else {
		logger.Info("All presets registered successfully")
	}

	globalManager = &Manager{
		agentManager: am,
	}

	logger.Info("Bot module initialized successfully")
	return nil
}

// GetAgentManager 获取 Agent 管理器
func GetAgentManager() *agent.AgentManager {
	return agent.GetAgentManager()
}

// GetManager 获取 Bot 管理器
func GetManager() *Manager {
	return globalManager
}
