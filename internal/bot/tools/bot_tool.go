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

func sanitizeName(name string) string {
	name = strings.ReplaceAll(name, " ", "_")
	var sb strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			sb.WriteRune(r)
		}
	}
	result := strings.ToLower(sb.String())
	if len(result) > 64 {
		result = result[:64]
	}
	return result
}

func sanitizeToolName(name string) string {
	cleaned := sanitizeName(name)
	if cleaned == "" {
		cleaned = "bot"
	}
	return "bot_" + cleaned
}

var _ tool.InvokableTool = (*botToolWrapper)(nil)

type botToolWrapper struct {
	tool.InvokableTool
}

func (w *botToolWrapper) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return w.InvokableTool.Info(ctx)
}
