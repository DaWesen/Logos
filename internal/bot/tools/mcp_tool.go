package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"Logos/internal/mcp_server"
	"Logos/pkg/logger"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

type MCPToolInput struct {
	ToolName string            `json:"tool_name" jsonschema:"description=要调用的MCP工具名称"`
	Params   map[string]string `json:"params" jsonschema:"description=工具调用参数"`
}

func NewMCPTool(mcpTool mcp_server.Tool) (tool.InvokableTool, error) {
	name := "mcp_" + mcpTool.Name()
	description := fmt.Sprintf("MCP工具: %s. %s", mcpTool.Name(), mcpTool.Description())

	t, err := utils.InferTool(name, description, func(ctx context.Context, input *MCPToolInput) (string, error) {
		logger.Info("MCPTool 被调用",
			logger.StringField("tool", mcpTool.Name()),
			logger.StringField("params_count", fmt.Sprintf("%d", len(input.Params))))

		result, err := mcpTool.Execute(ctx, input.Params)
		if err != nil {
			logger.Error("MCPTool 执行失败",
				logger.StringField("tool", mcpTool.Name()),
				logger.ErrorField(err))
			return fmt.Sprintf("工具 %s 执行失败: %s", mcpTool.Name(), err.Error()), nil
		}

		if result.IsError {
			return result.Content, nil
		}

		if len(result.Metadata) > 0 {
			metaJSON, _ := json.Marshal(result.Metadata)
			return fmt.Sprintf("%s\n\n[metadata]: %s", result.Content, string(metaJSON)), nil
		}

		return result.Content, nil
	})
	if err != nil {
		return nil, fmt.Errorf("创建 MCPTool 失败: %w", err)
	}

	return t, nil
}

func BuildMCPTools(registry *mcp_server.ToolRegistry) []tool.BaseTool {
	var tools []tool.BaseTool
	for _, mcpTool := range registry.List() {
		t, err := NewMCPTool(mcpTool)
		if err != nil {
			logger.Warn("创建 MCPTool 失败",
				logger.StringField("tool", mcpTool.Name()),
				logger.ErrorField(err))
			continue
		}
		tools = append(tools, t)
	}
	return tools
}
