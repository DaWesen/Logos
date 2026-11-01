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
	// buildAgent 构造 Agent 的注入点，默认 NewBaseBotAgent。
	// 独立成字段是为了能在测试里用「慢构造器」验证构造过程不持有全局锁。
	buildAgent func(cfg *AgentConfig, einoManager *eino.EinoManager) (BotAgent, error)
}

var (
	managerInstance *AgentManager
	managerOnce     sync.Once
)

func NewAgentManager(einoManager *eino.EinoManager) *AgentManager {
	return &AgentManager{
		einoManager: einoManager,
		agents:      make(map[string]BotAgent),
		buildAgent:  NewBaseBotAgent,
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
	logger.Info("创建 Bot Agent", logger.StringField("id", cfg.ID), logger.StringField("name", cfg.Name))

	// 只在读写 map 时持锁：NewBaseBotAgent 会读预设 prompt、装配模型与工具（含 IO），
	// 旧实现把它放在写锁内，一个慢构造会让 GetAgent/ListAgents 这些读操作全部排队，
	// 表现为「管理面板整体卡住」。
	m.mu.RLock()
	_, exists := m.agents[cfg.ID]
	m.mu.RUnlock()
	if exists {
		return nil, fmt.Errorf("agent with id %s already exists", cfg.ID)
	}

	agent, err := m.buildAgent(cfg, m.einoManager)
	if err != nil {
		logger.Error("创建 Bot Agent 失败", logger.ErrorField(err))
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	// 二次检查：并发创建同一 ID 时，后来者要放弃自己的实例，保证「一个 ID 一个 Agent」。
	if _, exists := m.agents[cfg.ID]; exists {
		return nil, fmt.Errorf("agent with id %s already exists", cfg.ID)
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
	m.mu.RLock()
	if agent, exists := m.agents[cfg.ID]; exists {
		m.mu.RUnlock()
		return agent, nil
	}
	m.mu.RUnlock()

	// 构造在锁外进行：并发首次请求同一 bot 时最多各建一次，
	// 用「偶尔多构造一个实例」换「构造期间不阻塞所有读」，代价可接受。
	agent, err := m.buildAgent(cfg, m.einoManager)
	if err != nil {
		logger.Error("创建 Bot Agent 失败", logger.ErrorField(err))
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, exists := m.agents[cfg.ID]; exists {
		return existing, nil
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
