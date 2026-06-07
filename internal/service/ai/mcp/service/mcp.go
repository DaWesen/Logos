package service

import (
	"context"
	"encoding/json"
	"fmt"

	"Logos/internal/mcp"
	"Logos/internal/mcp_server"
	"Logos/internal/service/ai/mcp/dao"
	"Logos/internal/service/ai/mcp/model"
	"Logos/pkg/eino"
	"Logos/pkg/logger"

	"github.com/google/uuid"
)

type MCPService interface {
	CallTool(ctx context.Context, toolID string, toolName string, parameters map[string]string) (string, map[string]string, error)
	RegisterTool(ctx context.Context, name string, description string, toolType int, parameters []ToolParameter, config map[string]string, userID string) (*model.Tool, error)
	ListTools(ctx context.Context, toolType *int, enabledOnly bool, userID string, page, pageSize int) ([]*model.Tool, int64, error)
	GetTool(ctx context.Context, toolID string) (*model.Tool, error)
	UpdateTool(ctx context.Context, toolID string, name string, description string, parameters []ToolParameter, config map[string]string, enabled bool) (*model.Tool, error)
	DeleteTool(ctx context.Context, toolID string) error
}

type ToolParameter struct {
	Name         string `json:"name"`
	Type         string `json:"type"`
	Description  string `json:"description"`
	Required     bool   `json:"required"`
	DefaultValue string `json:"default_value"`
}

type mcpServiceImpl struct {
	repo       dao.MCPRepository
	einoClient *eino.EinoManager
	registry   *mcp_server.ToolRegistry
}

func NewMCPService(repo dao.MCPRepository, einoClient *eino.EinoManager) MCPService {
	registry := mcp_server.DefaultRegistry()

	logger.Info("MCP工具引擎已初始化",
		logger.IntField("builtin_tools", len(registry.List())))

	return &mcpServiceImpl{
		repo:       repo,
		einoClient: einoClient,
		registry:   registry,
	}
}

func (s *mcpServiceImpl) CallTool(ctx context.Context, toolID string, toolName string, parameters map[string]string) (string, map[string]string, error) {
	logger.Info("调用工具", logger.StringField("tool_id", toolID), logger.StringField("tool_name", toolName))

	if builtinTool, ok := s.registry.Get(toolName); ok {
		result, err := builtinTool.Execute(ctx, parameters)
		if err != nil {
			return "", nil, err
		}

		status := "success"
		if result.IsError {
			status = "error"
		}

		callLog := &model.ToolCallLog{
			ID:       uuid.New().String(),
			ToolID:   toolName,
			ToolName: toolName,
			Result:   result.Content,
			Status:   status,
		}
		if parameters != nil {
			paramBytes, _ := json.Marshal(parameters)
			callLog.Params = string(paramBytes)
		}
		if logErr := s.repo.CreateCallLog(ctx, callLog); logErr != nil {
			logger.Warn("写入调用日志失败", logger.ErrorField(logErr))
		}

		return result.Content, result.Metadata, nil
	}

	var tool *model.Tool
	var err error
	if toolID != "" {
		tool, err = s.repo.GetTool(ctx, toolID)
	} else if toolName != "" {
		tool, err = s.repo.GetToolByName(ctx, toolName, "")
	}
	if err != nil {
		return "", nil, fmt.Errorf("查询工具失败: %w", err)
	}
	if tool == nil {
		return "", nil, fmt.Errorf("工具不存在")
	}
	if !tool.Enabled {
		return "", nil, fmt.Errorf("工具已禁用")
	}

	result, metadata, callErr := s.executeTool(ctx, tool, parameters)

	status := "success"
	if callErr != nil {
		status = "error"
	}

	callLog := &model.ToolCallLog{
		ID:       uuid.New().String(),
		ToolID:   tool.ID,
		ToolName: tool.Name,
		Result:   result,
		Status:   status,
	}
	if parameters != nil {
		paramBytes, _ := json.Marshal(parameters)
		callLog.Params = string(paramBytes)
	}
	if logErr := s.repo.CreateCallLog(ctx, callLog); logErr != nil {
		logger.Warn("写入调用日志失败", logger.ErrorField(logErr))
	}

	if callErr != nil {
		return "", nil, callErr
	}

	return result, metadata, nil
}

func (s *mcpServiceImpl) executeTool(ctx context.Context, tool *model.Tool, parameters map[string]string) (string, map[string]string, error) {
	if builtinTool, ok := s.registry.Get(tool.Name); ok {
		result, err := builtinTool.Execute(ctx, parameters)
		if err != nil {
			return "", nil, err
		}
		if result.IsError {
			return result.Content, result.Metadata, fmt.Errorf("%s", result.Content)
		}
		return result.Content, result.Metadata, nil
	}

	switch tool.Type {
	case 1:
		return s.executeWebSearch(ctx, tool, parameters)
	case 2:
		return s.executeCodeExecution(ctx, tool, parameters)
	case 3:
		return s.executeWeather(ctx, tool, parameters)
	case 4:
		return s.executeFileSystem(ctx, tool, parameters)
	case 5:
		return s.executeCustomTool(ctx, tool, parameters)
	default:
		return "", nil, fmt.Errorf("不支持的工具类型: %d", tool.Type)
	}
}

func (s *mcpServiceImpl) executeWebSearch(ctx context.Context, _ *model.Tool, params map[string]string) (string, map[string]string, error) {
	tool := &mcp_server.WebSearchTool{}
	result, err := tool.Execute(ctx, params)
	if err != nil {
		return "", nil, err
	}
	if result.IsError {
		return result.Content, result.Metadata, fmt.Errorf("%s", result.Content)
	}
	return result.Content, result.Metadata, nil
}

func (s *mcpServiceImpl) executeCodeExecution(ctx context.Context, _ *model.Tool, params map[string]string) (string, map[string]string, error) {
	tool := &mcp_server.CodeExecutionTool{}
	result, err := tool.Execute(ctx, params)
	if err != nil {
		return "", nil, err
	}
	if result.IsError {
		return result.Content, result.Metadata, fmt.Errorf("%s", result.Content)
	}
	return result.Content, result.Metadata, nil
}

func (s *mcpServiceImpl) executeWeather(ctx context.Context, _ *model.Tool, params map[string]string) (string, map[string]string, error) {
	tool := &mcp_server.WeatherTool{}
	result, err := tool.Execute(ctx, params)
	if err != nil {
		return "", nil, err
	}
	if result.IsError {
		return result.Content, result.Metadata, fmt.Errorf("%s", result.Content)
	}
	return result.Content, result.Metadata, nil
}

func (s *mcpServiceImpl) executeFileSystem(ctx context.Context, _ *model.Tool, params map[string]string) (string, map[string]string, error) {
	tool := &mcp_server.FileSystemTool{}
	result, err := tool.Execute(ctx, params)
	if err != nil {
		return "", nil, err
	}
	if result.IsError {
		return result.Content, result.Metadata, fmt.Errorf("%s", result.Content)
	}
	return result.Content, result.Metadata, nil
}

func (s *mcpServiceImpl) executeCustomTool(ctx context.Context, tool *model.Tool, params map[string]string) (string, map[string]string, error) {
	if s.einoClient == nil || !s.einoClient.HasChatModel() {
		return "自定义工具执行暂不可用", map[string]string{"status": "unavailable"}, nil
	}

	systemPrompt := tool.Description
	if systemPrompt == "" {
		systemPrompt = "你是一个工具执行助手。"
	}

	paramStr, _ := json.Marshal(params)
	prompt := fmt.Sprintf(`请执行以下工具调用：
工具名称: %s
工具描述: %s
调用参数: %s
请生成合理的执行结果。`, tool.Name, tool.Description, string(paramStr))

	response, err := s.einoClient.Chat(ctx, []string{systemPrompt, prompt})
	if err != nil {
		return "", nil, err
	}

	return response, map[string]string{"source": "llm", "tool_name": tool.Name}, nil
}

func (s *mcpServiceImpl) RegisterTool(ctx context.Context, name string, description string, toolType int, parameters []ToolParameter, config map[string]string, userID string) (*model.Tool, error) {
	logger.Info("注册工具", logger.StringField("name", name))

	existing, _ := s.repo.GetToolByName(ctx, name, userID)
	if existing != nil {
		return nil, fmt.Errorf("工具名称已存在: %s", name)
	}

	paramBytes, _ := json.Marshal(parameters)
	configBytes, _ := json.Marshal(config)

	tool := &model.Tool{
		ID:          uuid.New().String(),
		Name:        name,
		Description: description,
		Type:        toolType,
		Config:      string(configBytes),
		Parameters:  string(paramBytes),
		Enabled:     true,
		UserID:      userID,
	}

	if err := s.repo.CreateTool(ctx, tool); err != nil {
		return nil, fmt.Errorf("注册工具失败: %w", err)
	}

	return tool, nil
}

func (s *mcpServiceImpl) ListTools(ctx context.Context, toolType *int, enabledOnly bool, userID string, page, pageSize int) ([]*model.Tool, int64, error) {
	return s.repo.ListTools(ctx, toolType, enabledOnly, userID, page, pageSize)
}

func (s *mcpServiceImpl) GetTool(ctx context.Context, toolID string) (*model.Tool, error) {
	return s.repo.GetTool(ctx, toolID)
}

func (s *mcpServiceImpl) UpdateTool(ctx context.Context, toolID string, name string, description string, parameters []ToolParameter, config map[string]string, enabled bool) (*model.Tool, error) {
	logger.Info("更新工具", logger.StringField("id", toolID))

	tool, err := s.repo.GetTool(ctx, toolID)
	if err != nil {
		return nil, fmt.Errorf("查询工具失败: %w", err)
	}
	if tool == nil {
		return nil, fmt.Errorf("工具不存在")
	}

	if name != "" {
		tool.Name = name
	}
	if description != "" {
		tool.Description = description
	}
	if parameters != nil {
		paramBytes, _ := json.Marshal(parameters)
		tool.Parameters = string(paramBytes)
	}
	if config != nil {
		configBytes, _ := json.Marshal(config)
		tool.Config = string(configBytes)
	}
	tool.Enabled = enabled

	if err := s.repo.UpdateTool(ctx, tool); err != nil {
		return nil, fmt.Errorf("更新工具失败: %w", err)
	}

	return tool, nil
}

func (s *mcpServiceImpl) DeleteTool(ctx context.Context, toolID string) error {
	logger.Info("删除工具", logger.StringField("id", toolID))
	return s.repo.DeleteTool(ctx, toolID)
}

type MCPServiceService interface {
	CreateService(ctx context.Context, name, description, transportType, url string, headers, authConfig, advancedConfig map[string]string, enabled bool, userID string) (*model.MCPService, error)
	GetService(ctx context.Context, id string) (*model.MCPService, error)
	ListServices(ctx context.Context, enabledOnly bool, userID string, page, pageSize int) ([]*model.MCPService, int64, error)
	UpdateService(ctx context.Context, id string, name, description, transportType, url string, headers, authConfig, advancedConfig map[string]string, enabled bool) (*model.MCPService, error)
	DeleteService(ctx context.Context, id string) error
	TestConnection(ctx context.Context, url, transportType string, headers map[string]string, authConfig map[string]string) error
}

type mcpServiceServiceImpl struct {
	svcRepo   dao.MCPServiceRepository
	clientMgr *mcp.MCPClientManager
}

func NewMCPServiceService(svcRepo dao.MCPServiceRepository, clientMgr *mcp.MCPClientManager) MCPServiceService {
	return &mcpServiceServiceImpl{
		svcRepo:   svcRepo,
		clientMgr: clientMgr,
	}
}

func (s *mcpServiceServiceImpl) CreateService(ctx context.Context, name, description, transportType, url string, headers, authConfig, advancedConfig map[string]string, enabled bool, userID string) (*model.MCPService, error) {
	logger.Info("创建MCP服务", logger.StringField("name", name))

	existing, _ := s.svcRepo.GetServiceByName(ctx, name, userID)
	if existing != nil {
		return nil, fmt.Errorf("服务名称已存在: %s", name)
	}

	mergedHeaders := mergeHeadersWithAuthConfig(headers, authConfig)

	headersBytes, _ := json.Marshal(mergedHeaders)
	authConfigBytes, _ := json.Marshal(authConfig)
	advancedConfigBytes, _ := json.Marshal(advancedConfig)

	svc := &model.MCPService{
		ID:             uuid.New().String(),
		Name:           name,
		Description:    description,
		Enabled:        enabled,
		TransportType:  transportType,
		URL:            url,
		Headers:        string(headersBytes),
		AuthConfig:     string(authConfigBytes),
		AdvancedConfig: string(advancedConfigBytes),
		UserID:         userID,
	}

	if err := s.svcRepo.CreateService(ctx, svc); err != nil {
		return nil, fmt.Errorf("创建MCP服务失败: %w", err)
	}

	if enabled {
		s.clientMgr.GetOrCreateConnection(svc.ID, svc.URL, svc.TransportType, mergedHeaders)
	}

	return svc, nil
}

func (s *mcpServiceServiceImpl) GetService(ctx context.Context, id string) (*model.MCPService, error) {
	return s.svcRepo.GetService(ctx, id)
}

func (s *mcpServiceServiceImpl) ListServices(ctx context.Context, enabledOnly bool, userID string, page, pageSize int) ([]*model.MCPService, int64, error) {
	return s.svcRepo.ListServices(ctx, enabledOnly, userID, page, pageSize)
}

func (s *mcpServiceServiceImpl) UpdateService(ctx context.Context, id string, name, description, transportType, url string, headers, authConfig, advancedConfig map[string]string, enabled bool) (*model.MCPService, error) {
	logger.Info("更新MCP服务", logger.StringField("id", id))

	svc, err := s.svcRepo.GetService(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("查询MCP服务失败: %w", err)
	}
	if svc == nil {
		return nil, fmt.Errorf("MCP服务不存在")
	}

	if name != "" {
		svc.Name = name
	}
	if description != "" {
		svc.Description = description
	}
	if transportType != "" {
		svc.TransportType = transportType
	}
	if url != "" {
		svc.URL = url
	}

	var mergedHeaders map[string]string
	if headers != nil {
		mergedHeaders = mergeHeadersWithAuthConfig(headers, authConfig)
		headersBytes, _ := json.Marshal(mergedHeaders)
		svc.Headers = string(headersBytes)
	}
	if authConfig != nil {
		authConfigBytes, _ := json.Marshal(authConfig)
		svc.AuthConfig = string(authConfigBytes)
	}
	if advancedConfig != nil {
		advancedConfigBytes, _ := json.Marshal(advancedConfig)
		svc.AdvancedConfig = string(advancedConfigBytes)
	}
	svc.Enabled = enabled

	if err := s.svcRepo.UpdateService(ctx, svc); err != nil {
		return nil, fmt.Errorf("更新MCP服务失败: %w", err)
	}

	if enabled {
		s.clientMgr.GetOrCreateConnection(svc.ID, svc.URL, svc.TransportType, mergedHeaders)
	} else {
		s.clientMgr.RemoveConnection(svc.ID)
	}

	return svc, nil
}

func (s *mcpServiceServiceImpl) DeleteService(ctx context.Context, id string) error {
	logger.Info("删除MCP服务", logger.StringField("id", id))

	s.clientMgr.RemoveConnection(id)
	return s.svcRepo.DeleteService(ctx, id)
}

func (s *mcpServiceServiceImpl) TestConnection(ctx context.Context, url, transportType string, headers map[string]string, authConfig map[string]string) error {
	logger.Info("测试MCP服务连接", logger.StringField("url", url), logger.StringField("transport", transportType))
	mergedHeaders := mergeHeadersWithAuthConfig(headers, authConfig)
	return s.clientMgr.TestConnection(ctx, url, transportType, mergedHeaders)
}

func mergeHeadersWithAuthConfig(headers map[string]string, authConfig map[string]string) map[string]string {
	result := make(map[string]string, len(headers)+1)
	for k, v := range headers {
		result[k] = v
	}
	if authConfig == nil {
		return result
	}
	authType := authConfig["type"]
	switch authType {
	case "bearer":
		if token := authConfig["token"]; token != "" {
			result["Authorization"] = "Bearer " + token
		}
	case "api_key":
		key := authConfig["key"]
		headerName := authConfig["header_name"]
		if headerName == "" {
			headerName = "X-API-Key"
		}
		if key != "" {
			result[headerName] = key
		}
	}
	return result
}
