package coordinator

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"Logos/internal/bot/agent"
	"Logos/internal/bot/tools"
	"Logos/internal/mcp_server"
	"Logos/pkg/eino"
	"Logos/pkg/logger"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

const coordinatorInstruction = `你是一个智能协调助手，可以调用多个专业Bot和工具来帮助用户解决问题。

你的工作方式：
1. 分析用户的需求
2. 判断需要调用哪个Bot或工具
3. 可以连续调用多个Bot/工具来完成复杂任务
4. 将结果整合后回复用户

规则：
- 如果用户的问题只需要一个Bot/工具就能解决，直接调用它
- 如果用户的问题需要多步处理（如"总结并翻译"），按顺序调用多个Bot/工具
- 前一个工具的输出可以作为后一个工具的输入
- 如果没有合适的Bot/工具，直接用你的知识回答
- 当用户的问题涉及知识库中的专业内容时，优先使用 knowledge_search 和 grep_chunks 工具搜索
- 始终用中文回复`

type KnowledgeSearchService interface {
	tools.KnowledgeSearchService
}

type Coordinator struct {
	agent          adk.Agent
	runner         *adk.Runner
	einoMgr        *eino.EinoManager
	agentMgr       *agent.AgentManager
	mcpRegistry    *mcp_server.ToolRegistry
	knowledgeSvc   KnowledgeSearchService
	graphWriteSvc  tools.GraphWriteService
	graphSearchSvc tools.GraphService
	mu             sync.RWMutex
}

var (
	coordinatorInstance *Coordinator
	coordinatorOnce     sync.Once
)

func InitCoordinator(einoMgr *eino.EinoManager, agentMgr *agent.AgentManager, mcpRegistry *mcp_server.ToolRegistry) *Coordinator {
	coordinatorOnce.Do(func() {
		coordinatorInstance = &Coordinator{
			einoMgr:     einoMgr,
			agentMgr:    agentMgr,
			mcpRegistry: mcpRegistry,
		}
	})
	return coordinatorInstance
}

func InitCoordinatorWithKnowledge(einoMgr *eino.EinoManager, agentMgr *agent.AgentManager, mcpRegistry *mcp_server.ToolRegistry, knowledgeSvc KnowledgeSearchService) *Coordinator {
	coordinatorOnce.Do(func() {
		coordinatorInstance = &Coordinator{
			einoMgr:      einoMgr,
			agentMgr:     agentMgr,
			mcpRegistry:  mcpRegistry,
			knowledgeSvc: knowledgeSvc,
		}
	})
	return coordinatorInstance
}

func InitCoordinatorWithGraph(einoMgr *eino.EinoManager, agentMgr *agent.AgentManager, mcpRegistry *mcp_server.ToolRegistry, knowledgeSvc KnowledgeSearchService, graphWriteSvc tools.GraphWriteService, graphSearchSvc tools.GraphService) *Coordinator {
	coordinatorOnce.Do(func() {
		coordinatorInstance = &Coordinator{
			einoMgr:        einoMgr,
			agentMgr:       agentMgr,
			mcpRegistry:    mcpRegistry,
			knowledgeSvc:   knowledgeSvc,
			graphWriteSvc:  graphWriteSvc,
			graphSearchSvc: graphSearchSvc,
		}
	})
	return coordinatorInstance
}

func GetCoordinator() *Coordinator {
	return coordinatorInstance
}

func (c *Coordinator) Chat(ctx context.Context, message string) (string, error) {
	if err := c.ensureInitialized(ctx); err != nil {
		return "", err
	}

	logger.Info("Coordinator 收到请求", logger.StringField("message_len", fmt.Sprintf("%d", len(message))))

	messages := []*schema.Message{
		schema.UserMessage(message),
	}

	iter := c.runner.Run(ctx, messages)

	var fullResponse string
	for {
		event, ok := iter.Next()
		if !ok {
			break
		}
		if event != nil {
			msg, _, err := adk.GetMessage(event)
			if err == nil && msg != nil && msg.Content != "" {
				fullResponse += msg.Content
			}
		}
	}

	return fullResponse, nil
}

func (c *Coordinator) ChatStream(ctx context.Context, message string, onChunk func(string) error) error {
	if err := c.ensureInitialized(ctx); err != nil {
		return err
	}

	logger.Info("Coordinator 收到流式请求", logger.StringField("message_len", fmt.Sprintf("%d", len(message))))

	messages := []*schema.Message{
		schema.UserMessage(message),
	}

	iter := c.runner.Run(ctx, messages)

	for {
		event, ok := iter.Next()
		if !ok {
			break
		}
		if event != nil {
			msg, _, err := adk.GetMessage(event)
			if err == nil && msg != nil && msg.Content != "" {
				if err := onChunk(msg.Content); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

func (c *Coordinator) ensureInitialized(ctx context.Context) error {
	c.mu.RLock()
	if c.runner != nil {
		c.mu.RUnlock()
		return nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.runner != nil {
		return nil
	}

	return c.initialize(ctx)
}

func (c *Coordinator) initialize(ctx context.Context) error {
	if c.einoMgr == nil || !c.einoMgr.HasChatModel() {
		return fmt.Errorf("Eino ChatModel 未初始化，无法创建 Coordinator")
	}

	chatModel := c.einoMgr.GetChatModel()

	var allTools []tool.BaseTool

	if c.agentMgr != nil {
		agentIDs := c.agentMgr.ListAgents()
		var botAgents []agent.BotAgent
		for _, id := range agentIDs {
			a, err := c.agentMgr.GetAgent(id)
			if err != nil {
				continue
			}
			botAgents = append(botAgents, a)
		}
		botTools := tools.BuildBotTools(botAgents)
		allTools = append(allTools, botTools...)
		logger.Info("Coordinator 注册 Bot 工具", logger.IntField("count", len(botTools)))
	}

	if c.mcpRegistry != nil {
		mcpTools := tools.BuildMCPTools(c.mcpRegistry)
		allTools = append(allTools, mcpTools...)
		logger.Info("Coordinator 注册 MCP 工具", logger.IntField("count", len(mcpTools)))
	}

	if c.knowledgeSvc != nil {
		knowledgeTools := tools.BuildKnowledgeTools(c.knowledgeSvc)
		allTools = append(allTools, knowledgeTools...)
		logger.Info("Coordinator 注册知识库工具", logger.IntField("count", len(knowledgeTools)))
	}

	if c.graphSearchSvc != nil && c.graphWriteSvc != nil {
		graphSearchTools := tools.BuildGraphSearchTools(c.graphSearchSvc, "")
		graphWriteTools := tools.BuildGraphWriteTools(c.graphWriteSvc, c.graphSearchSvc, "")
		allTools = append(allTools, graphSearchTools...)
		allTools = append(allTools, graphWriteTools...)
		logger.Info("Coordinator 注册图谱工具", logger.IntField("search_count", len(graphSearchTools)), logger.IntField("write_count", len(graphWriteTools)))
	}

	toolDescriptions := c.buildToolDescriptions(allTools)
	instruction := coordinatorInstruction
	if len(toolDescriptions) > 0 {
		instruction += "\n\n可用的工具列表：\n" + toolDescriptions
	}

	coordinatorAgent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        "Coordinator",
		Description: "智能协调助手，可以调用多个专业Bot和工具来帮助用户解决问题",
		Instruction: instruction,
		Model:       chatModel,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools: allTools,
			},
		},
		MaxIterations: 10,
	})
	if err != nil {
		logger.Error("创建 Coordinator Agent 失败", logger.ErrorField(err))
		return fmt.Errorf("创建 Coordinator Agent 失败: %w", err)
	}

	c.agent = coordinatorAgent
	c.runner = adk.NewRunner(ctx, adk.RunnerConfig{Agent: coordinatorAgent})

	logger.Info("Coordinator Agent 初始化完成",
		logger.IntField("tools_count", len(allTools)))

	return nil
}

func (c *Coordinator) buildToolDescriptions(toolsList []tool.BaseTool) string {
	var sb strings.Builder
	for i, t := range toolsList {
		info, err := t.Info(context.Background())
		if err != nil {
			continue
		}
		sb.WriteString(fmt.Sprintf("%d. %s: %s\n", i+1, info.Name, info.Desc))
	}
	return sb.String()
}

func (c *Coordinator) Refresh(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.runner = nil
	c.agent = nil

	logger.Info("Coordinator Agent 已刷新，将在下次请求时重新初始化")
	return nil
}
