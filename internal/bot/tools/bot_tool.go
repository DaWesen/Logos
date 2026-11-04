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
	return newBotToolNamed(botAgent, botToolBaseName(botAgent.GetName()))
}

// newBotToolNamed 用调用方指定的函数名构造工具。
// 名字由装配方统一分配，才能在「多个 Bot 名清洗后塌缩成同一个」时做重名消解。
func newBotToolNamed(botAgent agent.BotAgent, name string) (tool.InvokableTool, error) {
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
	names := toolNameSet{}
	for _, a := range agents {
		t, err := newBotToolNamed(a, names.unique(botToolBaseName(a.GetName())))
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
	// 先 trim 再替换：纯空白名会被逐个替换成下划线（"   " → "___"），
	// 最终产出 bot____ 这种既不好看又可能与其他兜底名冲突的函数名。
	name = strings.TrimSpace(name)
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
	return botToolBaseName(name)
}

// botToolBaseName 生成 Bot 工具的完整函数名。
// 长度预算必须覆盖 "bot_" 前缀：旧实现先截断到 64 再拼前缀，最终 68 个字符，
// 超过供应商的函数名上限后整次请求 400，且只发生在「名字很长」这一小撮 Bot 上。
func botToolBaseName(name string) string {
	cleaned := sanitizeName(name)
	if cleaned == "" {
		cleaned = "bot"
	}
	return truncateToolName("bot_"+cleaned, maxToolNameRunes)
}

var _ tool.InvokableTool = (*botToolWrapper)(nil)

type botToolWrapper struct {
	tool.InvokableTool
}

func (w *botToolWrapper) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return w.InvokableTool.Info(ctx)
}
