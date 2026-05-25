package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"Logos/internal/mcp"
	"Logos/internal/mcp_server"
	"Logos/pkg/logger"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

type MCPToolInput struct {
	ToolName string            `json:"tool_name" jsonschema:"description=要调用的MCP工具名称"`
	Params   map[string]string `json:"params" jsonschema:"description=工具调用参数"`
}

func (e *MCPToolInput) UnmarshalJSON(data []byte) error {
	var raw struct {
		ToolName string          `json:"tool_name"`
		Params   json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	e.ToolName = raw.ToolName
	e.Params = make(map[string]string)

	if len(raw.Params) == 0 {
		return nil
	}

	if err := json.Unmarshal(raw.Params, &e.Params); err == nil {
		return nil
	}

	var paramStr string
	if err := json.Unmarshal(raw.Params, &paramStr); err != nil {
		return nil
	}

	json.Unmarshal([]byte(paramStr), &e.Params)
	return nil
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

type ExternalMCPToolInput struct {
	ToolName string                 `json:"tool_name" jsonschema:"description=要调用的外部MCP工具名称"`
	Params   map[string]interface{} `json:"params" jsonschema:"description=工具调用参数"`
}

func (e *ExternalMCPToolInput) UnmarshalJSON(data []byte) error {
	var raw struct {
		ToolName string          `json:"tool_name"`
		Params   json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	e.ToolName = raw.ToolName
	e.Params = make(map[string]interface{})

	if len(raw.Params) == 0 {
		return nil
	}

	if err := json.Unmarshal(raw.Params, &e.Params); err == nil {
		return nil
	}

	var paramStr string
	if err := json.Unmarshal(raw.Params, &paramStr); err != nil {
		return nil
	}

	json.Unmarshal([]byte(paramStr), &e.Params)
	return nil
}

func BuildExternalMCPTools(clientMgr *mcp.MCPClientManager, services []*ExternalMCPServiceInfo) []tool.BaseTool {
	var tools []tool.BaseTool
	for _, svc := range services {
		if !svc.Enabled {
			continue
		}
		listCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		toolInfos, err := clientMgr.ListTools(listCtx, svc.ID)
		cancel()
		if err != nil {
			logger.Warn("获取外部MCP服务工具列表失败",
				logger.StringField("service", svc.Name),
				logger.StringField("service_id", svc.ID),
				logger.ErrorField(err))
			continue
		}
		for _, toolInfo := range toolInfos {
			t, err := newExternalMCPTool(clientMgr, svc.ID, svc.Name, toolInfo)
			if err != nil {
				logger.Warn("创建外部MCPTool 失败",
					logger.StringField("tool", toolInfo.Name),
					logger.StringField("service", svc.Name),
					logger.ErrorField(err))
				continue
			}
			tools = append(tools, t)
		}
	}
	return tools
}

type ExternalMCPServiceInfo struct {
	ID      string
	Name    string
	Enabled bool
}

func newExternalMCPTool(clientMgr *mcp.MCPClientManager, serviceID, serviceName string, toolInfo mcp.MCPToolInfo) (tool.InvokableTool, error) {
	cleanSvcName := sanitizeName(serviceName)
	if cleanSvcName == "" {
		cleanSvcName = "svc"
	}
	cleanToolName := sanitizeName(toolInfo.Name)
	if cleanToolName == "" {
		cleanToolName = "tool"
	}
	name := fmt.Sprintf("ext_mcp_%s_%s", cleanSvcName, cleanToolName)
	description := fmt.Sprintf("外部MCP工具[%s/%s]: %s", serviceName, toolInfo.Name, toolInfo.Description)

	t, err := utils.InferTool(name, description, func(ctx context.Context, input *ExternalMCPToolInput) (string, error) {
		logger.Info("外部MCPTool 被调用",
			logger.StringField("service", serviceName),
			logger.StringField("tool", input.ToolName),
			logger.StringField("service_id", serviceID))

		result, err := clientMgr.CallTool(ctx, serviceID, input.ToolName, input.Params)
		if err != nil {
			logger.Error("外部MCPTool 执行失败",
				logger.StringField("service", serviceName),
				logger.StringField("tool", input.ToolName),
				logger.ErrorField(err))
			return fmt.Sprintf("外部工具 %s/%s 执行失败: %s", serviceName, input.ToolName, err.Error()), nil
		}

		if result.IsError {
			var texts []string
			for _, c := range result.Content {
				if c.Type == "text" {
					texts = append(texts, c.Text)
				}
			}
			return fmt.Sprintf("外部工具 %s/%s 执行错误: %s", serviceName, input.ToolName, strings.Join(texts, "\n")), nil
		}

		var texts []string
		for _, c := range result.Content {
			if c.Type == "text" {
				texts = append(texts, c.Text)
			}
		}
		return strings.Join(texts, "\n"), nil
	})
	if err != nil {
		return nil, fmt.Errorf("创建外部 MCPTool 失败: %w", err)
	}

	return t, nil
}
