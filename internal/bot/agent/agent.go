package agent

import (
	"context"
	"fmt"
	"io"
	"strings"

	"Logos/pkg/eino"
	"Logos/pkg/logger"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

type BotAgent interface {
	Chat(ctx context.Context, message string) (string, error)
	ChatStream(ctx context.Context, message string, onChunk func(string) error) error
	ChatWithHistory(ctx context.Context, messages []*schema.Message) (string, error)
	ChatStreamWithHistory(ctx context.Context, messages []*schema.Message, onChunk func(string) error) error
	GetName() string
	GetDescription() string
	GetSystemPrompt() string
}

type BaseBotAgent struct {
	id           string
	name         string
	description  string
	systemPrompt string
	runner       *adk.Runner
	streamRunner *adk.Runner
}

type AgentConfig struct {
	ID                string
	Name              string
	Description       string
	SystemPrompt      string
	ChatModel         model.BaseChatModel
	Tools             []tool.BaseTool
	KnowledgeBaseInfo string
	MaxIterations     int
}

func NewBaseBotAgent(cfg *AgentConfig, einoManager *eino.EinoManager) (BotAgent, error) {
	if einoManager == nil {
		return nil, fmt.Errorf("eino manager is nil")
	}

	chatModel := cfg.ChatModel
	if chatModel == nil {
		if !einoManager.HasChatModel() {
			logger.Error("ChatModel 未初始化，无法创建 Agent",
				logger.StringField("agentID", cfg.ID),
				logger.StringField("agentName", cfg.Name))
			return nil, fmt.Errorf("chat model not initialized - please configure API key in config.yaml or bot settings")
		}
		chatModel = einoManager.GetChatModel()
	}

	systemPrompt := cfg.SystemPrompt

	if len(cfg.Tools) > 0 {
		toolRules := "# 工具调用规则（最高优先级）\n" +
			"当你决定使用任何工具时，必须通过 function_call 机制直接调用，禁止仅用文字表达\"我来查一下\"\"让我搜索\"等意图而不实际调用。\n" +
			"你必须在同一轮回复中完成：调用工具 → 获取结果 → 基于结果回复用户。绝对不要在未实际调用工具的情况下告诉用户你将要使用工具。\n\n"
		systemPrompt = toolRules + systemPrompt
	}

	if cfg.KnowledgeBaseInfo != "" {
		systemPrompt = systemPrompt + cfg.KnowledgeBaseInfo
	}

	if len(cfg.Tools) > 0 {
		systemPrompt = systemPrompt + "\n\n【重要提醒】你必须使用可用工具来完成任务。如果需要查询信息、操作数据，立即调用对应的工具，不要只用文字回复而不调用工具。"
	}

	agentTools := cfg.Tools
	if agentTools == nil {
		agentTools = []tool.BaseTool{}
	}

	maxIter := cfg.MaxIterations
	if maxIter <= 0 {
		maxIter = 10
	}

	logger.Info("创建 Bot Agent",
		logger.StringField("id", cfg.ID),
		logger.StringField("name", cfg.Name),
		logger.IntField("tools_count", len(agentTools)),
		logger.IntField("max_iterations", maxIter),
		logger.BoolField("has_knowledge_base", cfg.KnowledgeBaseInfo != ""))

	agent, err := adk.NewChatModelAgent(context.Background(), &adk.ChatModelAgentConfig{
		Name:        cfg.Name,
		Description: cfg.Description,
		Instruction: systemPrompt,
		Model:       chatModel,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools: agentTools,
				UnknownToolsHandler: func(ctx context.Context, toolName, toolInput string) (string, error) {
					logger.Warn("LLM调用了未注册的工具",
						logger.StringField("tool", toolName),
						logger.StringField("input", toolInput))
					return fmt.Sprintf("工具 %s 暂时不可用，请使用其他可用工具完成任务。", toolName), nil
				},
			},
		},
		MaxIterations: maxIter,
	})
	if err != nil {
		logger.Error("创建 ChatModelAgent 失败", logger.ErrorField(err))
		return nil, err
	}

	runner := adk.NewRunner(context.Background(), adk.RunnerConfig{Agent: agent})
	streamRunner := adk.NewRunner(context.Background(), adk.RunnerConfig{
		Agent:           agent,
		EnableStreaming: true,
	})

	return &BaseBotAgent{
		id:           cfg.ID,
		name:         cfg.Name,
		description:  cfg.Description,
		systemPrompt: systemPrompt,
		runner:       runner,
		streamRunner: streamRunner,
	}, nil
}

func (a *BaseBotAgent) Chat(ctx context.Context, message string) (string, error) {
	return a.chatWithMessages(ctx, a.buildMessages(message, nil))
}

func (a *BaseBotAgent) ChatStream(ctx context.Context, message string, onChunk func(string) error) error {
	return a.chatStreamWithMessages(ctx, a.buildMessages(message, nil), onChunk)
}

func (a *BaseBotAgent) ChatWithHistory(ctx context.Context, messages []*schema.Message) (string, error) {
	return a.chatWithMessages(ctx, a.buildMessages("", messages))
}

func (a *BaseBotAgent) ChatStreamWithHistory(ctx context.Context, messages []*schema.Message, onChunk func(string) error) error {
	return a.chatStreamWithMessages(ctx, a.buildMessages("", messages), onChunk)
}

func (a *BaseBotAgent) GetName() string         { return a.name }
func (a *BaseBotAgent) GetDescription() string  { return a.description }
func (a *BaseBotAgent) GetSystemPrompt() string { return a.systemPrompt }

func (a *BaseBotAgent) chatWithMessages(ctx context.Context, messages []*schema.Message) (string, error) {
	if a.runner == nil {
		return "", fmt.Errorf("agent runner not initialized")
	}

	logger.Info("Bot chat request",
		logger.StringField("bot", a.name),
		logger.IntField("messages_count", len(messages)))

	iter := a.runner.Run(ctx, messages)

	var lastValidContent string
	eventCount := 0

	for {
		event, ok := iter.Next()
		if !ok {
			break
		}
		eventCount++

		if event == nil {
			continue
		}

		if event.Err != nil {
			logger.Error("Agent事件错误",
				logger.IntField("event_index", eventCount),
				logger.ErrorField(event.Err))
			continue
		}

		if event.Action != nil && event.Action.Exit {
			break
		}

		if event.Output == nil || event.Output.MessageOutput == nil {
			continue
		}

		if event.Output.MessageOutput.Role != schema.Assistant {
			continue
		}

		msg, _, err := adk.GetMessage(event)
		if err != nil {
			logger.Warn("获取事件消息失败",
				logger.IntField("event_index", eventCount),
				logger.ErrorField(err))
			continue
		}

		if msg == nil {
			continue
		}

		if len(msg.ToolCalls) > 0 {
			logger.Debug("跳过中间Assistant消息",
				logger.IntField("event_index", eventCount),
				logger.IntField("tool_calls", len(msg.ToolCalls)))
			continue
		}

		if msg.Content != "" {
			lastValidContent = msg.Content
		}
	}

	logger.Info("Bot chat response",
		logger.StringField("bot", a.name),
		logger.IntField("response_len", len(lastValidContent)),
		logger.IntField("events", eventCount))

	return lastValidContent, nil
}

func (a *BaseBotAgent) chatStreamWithMessages(ctx context.Context, messages []*schema.Message, onChunk func(string) error) error {
	if a.streamRunner == nil {
		return fmt.Errorf("agent stream runner not initialized")
	}

	logger.Info("Bot stream chat request",
		logger.StringField("bot", a.name),
		logger.IntField("messages_count", len(messages)))

	iter := a.streamRunner.Run(ctx, messages)

	var buffer strings.Builder
	seenToolEvent := false

	for {
		event, ok := iter.Next()
		if !ok {
			break
		}
		if event == nil {
			continue
		}

		if event.Err != nil {
			logger.Error("Agent流式事件错误", logger.ErrorField(event.Err))
			continue
		}

		if event.Action != nil && event.Action.Exit {
			break
		}

		if event.Output == nil || event.Output.MessageOutput == nil {
			continue
		}

		mv := event.Output.MessageOutput

		if mv.Role == schema.Tool {
			seenToolEvent = true
			buffer.Reset()
			continue
		}

		if mv.Role != schema.Assistant {
			continue
		}

		msg, _, err := adk.GetMessage(event)
		if err != nil {
			logger.Error("获取流式事件消息失败", logger.ErrorField(err))
			continue
		}

		if msg == nil {
			continue
		}

		if len(msg.ToolCalls) > 0 {
			buffer.Reset()
			continue
		}

		if seenToolEvent {
			if mv.IsStreaming && mv.MessageStream != nil {
				for {
					chunk, recvErr := mv.MessageStream.Recv()
					if recvErr == io.EOF {
						break
					}
					if recvErr != nil {
						logger.Error("读取流式chunk失败", logger.ErrorField(recvErr))
						break
					}
					if chunk.Content != "" {
						if sendErr := onChunk(chunk.Content); sendErr != nil {
							return sendErr
						}
					}
				}
			} else if msg.Content != "" {
				if sendErr := onChunk(msg.Content); sendErr != nil {
					return sendErr
				}
			}
		} else {
			if msg.Content != "" {
				buffer.WriteString(msg.Content)
			}
		}
	}

	if buffer.Len() > 0 {
		if err := onChunk(buffer.String()); err != nil {
			return err
		}
	}

	return nil
}

func (a *BaseBotAgent) buildMessages(currentMessage string, history []*schema.Message) []*schema.Message {
	var messages []*schema.Message

	if a.systemPrompt != "" {
		messages = append(messages, schema.SystemMessage(a.systemPrompt))
	}

	if len(history) > 0 {
		messages = append(messages, history...)
	}

	if currentMessage != "" {
		messages = append(messages, schema.UserMessage(currentMessage))
	}

	return messages
}
