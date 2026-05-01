package agent

import (
	"context"
	"fmt"
	"sync"

	"Logos/pkg/eino"
	"Logos/pkg/logger"
)

type AgentManager struct {
	einoManager *eino.EinoManager
	agents      map[string]BotAgent
	mu          sync.RWMutex
}

var (
	managerInstance *AgentManager
	managerOnce     sync.Once
)

func NewAgentManager(einoManager *eino.EinoManager) *AgentManager {
	return &AgentManager{
		einoManager: einoManager,
		agents:      make(map[string]BotAgent),
	}
}

func InitAgentManager(einoManager *eino.EinoManager) *AgentManager {
	managerOnce.Do(func() {
		managerInstance = NewAgentManager(einoManager)
	})
	return managerInstance
}

func GetAgentManager() *AgentManager {
	return managerInstance
}

func (m *AgentManager) CreateAgent(cfg *AgentConfig) (BotAgent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	logger.Info("创建 Bot Agent", logger.StringField("id", cfg.ID), logger.StringField("name", cfg.Name))

	if _, exists := m.agents[cfg.ID]; exists {
		return nil, fmt.Errorf("agent with id %s already exists", cfg.ID)
	}

	agent, err := NewBaseBotAgent(cfg, m.einoManager)
	if err != nil {
		logger.Error("创建 Bot Agent 失败", logger.ErrorField(err))
		return nil, err
	}

	m.agents[cfg.ID] = agent

	logger.Info("Bot Agent 创建成功", logger.StringField("id", cfg.ID))
	return agent, nil
}

func (m *AgentManager) GetAgent(id string) (BotAgent, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	agent, exists := m.agents[id]
	if !exists {
		return nil, fmt.Errorf("agent with id %s not found", id)
	}

	return agent, nil
}

func (m *AgentManager) GetOrCreateAgent(cfg *AgentConfig) (BotAgent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if agent, exists := m.agents[cfg.ID]; exists {
		return agent, nil
	}

	agent, err := NewBaseBotAgent(cfg, m.einoManager)
	if err != nil {
		logger.Error("创建 Bot Agent 失败", logger.ErrorField(err))
		return nil, err
	}

	m.agents[cfg.ID] = agent

	logger.Info("Bot Agent 创建并缓存", logger.StringField("id", cfg.ID))
	return agent, nil
}

func (m *AgentManager) DeleteAgent(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.agents[id]; !exists {
		return fmt.Errorf("agent with id %s not found", id)
	}

	delete(m.agents, id)

	logger.Info("Bot Agent 已删除", logger.StringField("id", id))
	return nil
}

func (m *AgentManager) InvalidateAgent(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.agents, id)
}

func (m *AgentManager) ListAgents() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ids := make([]string, 0, len(m.agents))
	for id := range m.agents {
		ids = append(ids, id)
	}
	return ids
}

func (m *AgentManager) RegisterPresetAgent(cfg *AgentConfig) (BotAgent, error) {
	return m.CreateAgent(cfg)
}

func (m *AgentManager) ChatWithAgent(ctx context.Context, id, message string) (string, error) {
	agent, err := m.GetAgent(id)
	if err != nil {
		return "", err
	}
	return agent.Chat(ctx, message)
}

func (m *AgentManager) ChatStreamWithAgent(ctx context.Context, id, message string, onChunk func(string) error) error {
	agent, err := m.GetAgent(id)
	if err != nil {
		return err
	}
	return agent.ChatStream(ctx, message, onChunk)
}
