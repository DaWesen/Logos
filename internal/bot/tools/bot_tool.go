package tools

import (
	"context"
	"fmt"
	"strings"

	"Logos/internal/bot/agent"
	"Logos/pkg/logger"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"
)

type BotToolInput struct {
	Message string `json:"message" jsonschema:"description=要发送给该Bot的消息内容"`
}

func NewBotTool(botAgent agent.BotAgent) (tool.InvokableTool, error) {
	name := sanitizeToolName(botAgent.GetName())
	description := fmt.Sprintf("Bot: %s. %s", botAgent.GetName(), botAgent.GetDescription())

	t, err := utils.InferTool(name, description, func(ctx context.Context, input *BotToolInput) (string, error) {
		logger.Info("BotTool 被调用",
			logger.StringField("bot", botAgent.GetName()),
			logger.StringField("message_len", fmt.Sprintf("%d", len(input.Message))))

		resp, err := botAgent.Chat(ctx, input.Message)
		if err != nil {
			logger.Error("BotTool 调用失败",
				logger.StringField("bot", botAgent.GetName()),
				logger.ErrorField(err))
			return fmt.Sprintf("Bot %s 调用失败: %s", botAgent.GetName(), err.Error()), nil
		}

		return resp, nil
	})
	if err != nil {
		return nil, fmt.Errorf("创建 BotTool 失败: %w", err)
	}

	return t, nil
}

func BuildBotTools(agents []agent.BotAgent) []tool.BaseTool {
	var tools []tool.BaseTool
	for _, a := range agents {
		t, err := NewBotTool(a)
		if err != nil {
			logger.Warn("创建 BotTool 失败",
				logger.StringField("bot", a.GetName()),
				logger.ErrorField(err))
			continue
		}
		tools = append(tools, t)
	}
	return tools
}

func sanitizeToolName(name string) string {
	name = strings.ReplaceAll(name, " ", "_")
	name = strings.ReplaceAll(name, "-", "_")
	name = strings.ToLower(name)
	if len(name) > 64 {
		name = name[:64]
	}
	if len(name) == 0 {
		name = "bot"
	}
	return "bot_" + name
}

var _ tool.InvokableTool = (*botToolWrapper)(nil)

type botToolWrapper struct {
	tool.InvokableTool
}

func (w *botToolWrapper) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return w.InvokableTool.Info(ctx)
}
