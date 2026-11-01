package coordinator

import (
	"context"
	"fmt"
	"io"
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

// eventIterator 事件源抽象（adk.AsyncIterator 天然满足），只为让消费逻辑可单测。
type eventIterator interface {
	Next() (*adk.AgentEvent, bool)
}

type coordRunSource func(ctx context.Context, messages []*schema.Message) eventIterator

type Coordinator struct {
	agent adk.Agent
	// run / stream 分开：流式接口必须用 EnableStreaming 的 Runner，
	// 复用非流式 Runner 的话事件里根本没有 MessageStream，只能整段一次性推送。
	run            coordRunSource
	stream         coordRunSource
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
	if c.run == nil {
		return "", fmt.Errorf("coordinator runner not initialized")
	}

	logger.Info("Coordinator 收到请求", logger.StringField("message_len", fmt.Sprintf("%d", len(message))))

	messages := []*schema.Message{
		schema.UserMessage(message),
	}

	iter := c.run(ctx, messages)

	var (
		fullResponse string
		firstErr     error
	)

	for {
		event, ok := iter.Next()
		if !ok {
			break
		}
		if event == nil {
			continue
		}
		if event.Err != nil {
			// 协调链路是「模型自己决定调哪些 Bot」，模型报错时上层拿到的必须是错误，
			// 否则用户会收到一条空白回答，而监控上显示这次协调成功。
			if firstErr == nil {
				firstErr = event.Err
			}
			logger.Error("Coordinator 事件错误", logger.ErrorField(event.Err))
			continue
		}
		if event.Output == nil || event.Output.MessageOutput == nil {
			continue
		}

		mv := event.Output.MessageOutput
		if mv.Role != schema.Assistant {
			closeMessageStream(mv)
			continue
		}

		msg, _, err := adk.GetMessage(event)
		if mv.IsStreaming {
			closeMessageStream(mv)
		}
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if msg == nil {
			continue
		}

		eino.RecordUsageFrom(ctx, msg)
		// 带 tool_calls 的中间消息不是答案，拼接它会把「我准备调用 xx」混进最终回答
		if len(msg.ToolCalls) > 0 {
			continue
		}
		fullResponse += msg.Content
	}

	if fullResponse == "" {
		if firstErr != nil {
			return "", fmt.Errorf("coordinator 运行失败: %w", firstErr)
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		return "", fmt.Errorf("coordinator 未产出任何内容")
	}

	return fullResponse, nil
}

func (c *Coordinator) ChatStream(ctx context.Context, message string, onChunk func(string) error) error {
	if err := c.ensureInitialized(ctx); err != nil {
		return err
	}
	if c.stream == nil {
		return fmt.Errorf("coordinator stream runner not initialized")
	}

	logger.Info("Coordinator 收到流式请求", logger.StringField("message_len", fmt.Sprintf("%d", len(message))))

	messages := []*schema.Message{
		schema.UserMessage(message),
	}

	iter := c.stream(ctx, messages)

	var (
		emitted  int
		firstErr error
	)

	for {
		event, ok := iter.Next()
		if !ok {
			break
		}
		if event == nil {
			continue
		}
		if event.Err != nil {
			if firstErr == nil {
				firstErr = event.Err
			}
			logger.Error("Coordinator 流式事件错误", logger.ErrorField(event.Err))
			continue
		}
		if event.Output == nil || event.Output.MessageOutput == nil {
			continue
		}

		mv := event.Output.MessageOutput
		if mv.Role != schema.Assistant {
			// 工具结果（Role=Tool）的正文是给模型看的原始数据，绝不能推给用户
			closeMessageStream(mv)
			continue
		}

		if mv.IsStreaming && mv.MessageStream != nil {
			// 真流式：边收边推，而不是先整段拼完再一次性刷出去
			stream := mv.MessageStream
			for {
				chunk, recvErr := stream.Recv()
				if recvErr == io.EOF {
					break
				}
				if recvErr != nil {
					if firstErr == nil {
						firstErr = recvErr
					}
					logger.Error("Coordinator 读取流式chunk失败", logger.ErrorField(recvErr))
					break
				}
				if chunk == nil {
					continue
				}
				eino.RecordUsageFrom(ctx, chunk)
				if chunk.Content == "" {
					continue
				}
				emitted += len(chunk.Content)
				if err := onChunk(chunk.Content); err != nil {
					stream.Close()
					return err
				}
			}
			stream.Close()
			continue
		}

		if mv.Message == nil {
			continue
		}
		eino.RecordUsageFrom(ctx, mv.Message)
		if len(mv.Message.ToolCalls) > 0 || mv.Message.Content == "" {
			continue
		}
		emitted += len(mv.Message.Content)
		if err := onChunk(mv.Message.Content); err != nil {
			return err
		}
	}

	if emitted == 0 {
		if firstErr != nil {
			return fmt.Errorf("coordinator 流式运行失败: %w", firstErr)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return fmt.Errorf("coordinator 未产出任何内容")
	}

	return nil
}

// closeMessageStream 关闭不再消费的消息流（幂等，nil 安全）。
func closeMessageStream(mv *adk.MessageVariant) {
	if mv == nil || mv.MessageStream == nil {
		return
	}
	mv.MessageStream.Close()
}

func (c *Coordinator) ensureInitialized(ctx context.Context) error {
	c.mu.RLock()
	if c.run != nil {
		c.mu.RUnlock()
		return nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.run != nil {
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

	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: coordinatorAgent})
	streamRunner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: coordinatorAgent, EnableStreaming: true})

	c.agent = coordinatorAgent
	c.run = func(ctx context.Context, messages []*schema.Message) eventIterator {
		return runner.Run(ctx, messages)
	}
	c.stream = func(ctx context.Context, messages []*schema.Message) eventIterator {
		return streamRunner.Run(ctx, messages)
	}

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

	c.run = nil
	c.stream = nil
	c.agent = nil

	logger.Info("Coordinator Agent 已刷新，将在下次请求时重新初始化")
	return nil
}
