package mcp_server

import (
	"context"
)

type ToolResult struct {
	Content  string
	Metadata map[string]string
	IsError  bool
}

type Tool interface {
	Name() string
	Description() string
	Type() int
	Parameters() []ToolParamDef
	Execute(ctx context.Context, params map[string]string) (*ToolResult, error)
}

type ToolParamDef struct {
	Name         string `json:"name"`
	Type         string `json:"type"`
	Description  string `json:"description"`
	Required     bool   `json:"required"`
	DefaultValue string `json:"default_value"`
}

type ToolRegistry struct {
	tools map[string]Tool
}

func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		tools: make(map[string]Tool),
	}
}

func (r *ToolRegistry) Register(tool Tool) {
	r.tools[tool.Name()] = tool
}

func (r *ToolRegistry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

func (r *ToolRegistry) List() []Tool {
	var result []Tool
	for _, t := range r.tools {
		result = append(result, t)
	}
	return result
}

func (r *ToolRegistry) ListByType(toolType int) []Tool {
	var result []Tool
	for _, t := range r.tools {
		if t.Type() == toolType {
			result = append(result, t)
		}
	}
	return result
}

func DefaultRegistry() *ToolRegistry {
	registry := NewToolRegistry()

	registry.Register(&WebSearchTool{})
	registry.Register(&CodeExecutionTool{})
	registry.Register(&WeatherTool{})
	registry.Register(&FileSystemTool{})
	registry.Register(&CalculatorTool{})
	registry.Register(&TimeTool{})
	registry.Register(&HTTPTemplateTool{})

	return registry
}
