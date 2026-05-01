package agent

import (
	"context"
	"fmt"

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
}

type AgentConfig struct {
	ID           string
	Name         string
	Description  string
	SystemPrompt string
	ChatModel    model.BaseChatModel
}

func NewBaseBotAgent(cfg *AgentConfig, einoManager *eino.EinoManager) (BotAgent, error) {
	if einoManager == nil {
		return nil, fmt.Errorf("eino manager is nil")
	}

	chatModel := cfg.ChatModel
	if chatModel == nil {
		if !einoManager.HasChatModel() {
			return nil, fmt.Errorf("chat model not initialized")
		}
		chatModel = einoManager.GetChatModel()
	}

	agent, err := adk.NewChatModelAgent(context.Background(), &adk.ChatModelAgentConfig{
		Name:        cfg.Name,
		Description: cfg.Description,
		Instruction: cfg.SystemPrompt,
		Model:       chatModel,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools: []tool.BaseTool{},
			},
		},
	})
	if err != nil {
		logger.Error("创建 ChatModelAgent 失败", logger.ErrorField(err))
		return nil, err
	}

	runner := adk.NewRunner(context.Background(), adk.RunnerConfig{Agent: agent})

	return &BaseBotAgent{
		id:           cfg.ID,
		name:         cfg.Name,
		description:  cfg.Description,
		systemPrompt: cfg.SystemPrompt,
		runner:       runner,
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

func (a *BaseBotAgent) GetName() string        { return a.name }
func (a *BaseBotAgent) GetDescription() string  { return a.description }
func (a *BaseBotAgent) GetSystemPrompt() string { return a.systemPrompt }

func (a *BaseBotAgent) chatWithMessages(ctx context.Context, messages []*schema.Message) (string, error) {
	if a.runner == nil {
		return "", fmt.Errorf("agent runner not initialized")
	}

	logger.Info("Bot chat request", logger.StringField("bot", a.name))

	iter := a.runner.Run(ctx, messages)

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

	logger.Info("Bot chat response", logger.StringField("bot", a.name))
	return fullResponse, nil
}

func (a *BaseBotAgent) chatStreamWithMessages(ctx context.Context, messages []*schema.Message, onChunk func(string) error) error {
	if a.runner == nil {
		return fmt.Errorf("agent runner not initialized")
	}

	logger.Info("Bot stream chat request", logger.StringField("bot", a.name))

	iter := a.runner.Run(ctx, messages)

	for {
		event, ok := iter.Next()
		if !ok {
			break
		}
		if event != nil {
			msg, _, err := adk.GetMessage(event)
			if err == nil && msg != nil && msg.Content != "" {
				if err := onChunk(msg.Content); err != nil {
					logger.Error("处理流式 chunk 失败", logger.ErrorField(err))
					return err
				}
			}
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
