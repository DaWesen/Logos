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
	e.Params = decodeStringParams(raw.Params)
	return nil
}

// decodeStringParams 把模型给的 params 归一化成 map[string]string。
//
// 模型不会严格遵守 schema：`{"limit": 10}`、`{"enabled": true}`、
// 甚至把对象再序列化成字符串（双重编码）都很常见。旧实现按 map[string]string
// 解析，失败后**不报错、返回空 map**——工具在「完全没有参数」的状态下执行，
// 模型却以为自己的参数生效了，错误只在结果里绕一圈才被发现。
func decodeStringParams(raw json.RawMessage) map[string]string {
	params := make(map[string]string)
	if len(raw) == 0 {
		return params
	}

	var obj map[string]interface{}
	if err := json.Unmarshal(raw, &obj); err != nil {
		// 双重编码：params 本身是一个 JSON 字符串
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return params
		}
		if json.Unmarshal([]byte(s), &obj) != nil {
			return params
		}
	}

	for k, v := range obj {
		switch t := v.(type) {
		case string:
			params[k] = t
		case nil:
			// null 不传，避免把 "null" 当字符串塞进工具
		default:
			// 数字/布尔统一转成 JSON 字面量（10 → "10"，true → "true"），
			// 对象/数组同理保留结构，工具侧可自行再解析。
			if b, err := json.Marshal(t); err == nil {
				params[k] = string(b)
			}
		}
	}
	return params
}

func NewMCPTool(mcpTool mcp_server.Tool) (tool.InvokableTool, error) {
	return newMCPToolNamed(mcpTool, "mcp_"+mcpTool.Name())
}

// newMCPToolNamed 用调用方指定的函数名构造工具：名字由装配层统一分配，
// 才能在不改变工具实现的前提下做长度收敛与重名消解。
func newMCPToolNamed(mcpTool mcp_server.Tool, name string) (tool.InvokableTool, error) {
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
	names := toolNameSet{}
	for _, mcpTool := range registry.List() {
		t, err := newMCPToolNamed(mcpTool, names.unique("mcp_"+mcpTool.Name()))
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
	names := toolNameSet{}
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
			// 服务名/tool 名经过 sanitize 后可能塌缩成同一个（"搜索 AI" 与 "搜索AI"），
			// 直接拼接会产出重名函数：模型看到两个同名工具，注册表也可能被后一个覆盖。
			name := names.unique(externalToolBaseName(svc.Name, toolInfo.Name))
			t, err := newExternalMCPTool(clientMgr, svc.ID, svc.Name, toolInfo, name)
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

// externalToolBaseName 依据服务名与工具名生成函数名基底（未做长度与去重处理）。
func externalToolBaseName(serviceName, toolName string) string {
	cleanSvcName := sanitizeName(serviceName)
	if cleanSvcName == "" {
		cleanSvcName = "svc"
	}
	cleanToolName := sanitizeName(toolName)
	if cleanToolName == "" {
		cleanToolName = "tool"
	}
	return fmt.Sprintf("ext_mcp_%s_%s", cleanSvcName, cleanToolName)
}

func newExternalMCPTool(clientMgr *mcp.MCPClientManager, serviceID, serviceName string, toolInfo mcp.MCPToolInfo, name string) (tool.InvokableTool, error) {
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

// maxToolNameRunes 供应商侧函数名长度上限（OpenAI 为 64 字符）。
// 超长名字会被截断甚至直接 400；而截断本身又会让两个不同工具塌缩成同名，
// 所以「截断」与「去重」必须一起做，只做其中一个都解决不了问题。
const maxToolNameRunes = 64

// toolNameSet 为一次工具装配分配互不冲突的函数名。
type toolNameSet map[string]struct{}

func (s toolNameSet) unique(base string) string {
	candidate := truncateToolName(base, maxToolNameRunes)
	if _, exists := s[candidate]; !exists {
		s[candidate] = struct{}{}
		return candidate
	}
	for i := 2; ; i++ {
		suffix := fmt.Sprintf("_%d", i)
		candidate = truncateToolName(base, maxToolNameRunes-len([]rune(suffix))) + suffix
		if _, exists := s[candidate]; !exists {
			s[candidate] = struct{}{}
			return candidate
		}
	}
}

func truncateToolName(name string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	r := []rune(name)
	if len(r) <= maxRunes {
		return name
	}
	return string(r[:maxRunes])
}
