package handler

import (
	"context"
	"encoding/json"
	"fmt"

	"Logos/internal/service/ai/mcp/model"
	"Logos/internal/service/ai/mcp/service"
	"Logos/pkg/auth"
	"Logos/pkg/logger"
	"Logos/pkg/pagination"
	pb "Logos/proto_gen/mcp"

	"google.golang.org/protobuf/types/known/timestamppb"
)

func getUserIDFromContext(ctx context.Context) string {
	userID, err := auth.GetUserID(ctx)
	if err != nil {
		return ""
	}
	return userID
}

type MCPServiceImpl struct {
	pb.UnimplementedMCPServiceServer
	MCPService    service.MCPService
	MCPServiceSvc service.MCPServiceService
}

func (s *MCPServiceImpl) CallTool(ctx context.Context, req *pb.CallToolRequest) (*pb.CallToolResponse, error) {
	resp := &pb.CallToolResponse{}

	result, metadata, err := s.MCPService.CallTool(ctx, req.ToolId, req.ToolName, req.Parameters)
	if err != nil {
		logger.Error("调用工具失败", logger.ErrorField(err))
		resp.Code = 1
		resp.Message = err.Error()
		return resp, nil
	}

	resp.Code = 0
	resp.Message = "success"
	resp.Result = result
	resp.Metadata = metadata
	return resp, nil
}

func (s *MCPServiceImpl) RegisterTool(ctx context.Context, req *pb.RegisterToolRequest) (*pb.RegisterToolResponse, error) {
	resp := &pb.RegisterToolResponse{}

	var params []service.ToolParameter
	for _, p := range req.Parameters {
		params = append(params, service.ToolParameter{
			Name:         p.Name,
			Type:         p.Type,
			Description:  p.Description,
			Required:     p.Required,
			DefaultValue: p.DefaultValue,
		})
	}

	tool, err := s.MCPService.RegisterTool(ctx, req.Name, req.Description, int(req.Type), params, req.Config, getUserIDFromContext(ctx))
	if err != nil {
		logger.Error("注册工具失败", logger.ErrorField(err))
		resp.Code = 1
		resp.Message = err.Error()
		return resp, nil
	}

	resp.Code = 0
	resp.Message = "success"
	data, convErr := convertModelToolToProtoTool(tool)
	if convErr != nil {
		logger.Error("转换工具信息失败",
			logger.StringField("id", tool.ID),
			logger.ErrorField(convErr))
		resp.Code = 1
		resp.Message = convErr.Error()
		return resp, nil
	}
	resp.Data = data
	return resp, nil
}

func (s *MCPServiceImpl) ListTools(ctx context.Context, req *pb.ListToolsRequest) (*pb.ListToolsResponse, error) {
	resp := &pb.ListToolsResponse{}

	var toolType *int
	if req.Type != 0 {
		t := int(req.Type)
		toolType = &t
	}

	page, pageSize := pagination.Normalize(int(req.Page), int(req.PageSize))

	tools, total, err := s.MCPService.ListTools(ctx, toolType, req.EnabledOnly, getUserIDFromContext(ctx), page, pageSize)
	if err != nil {
		logger.Error("获取工具列表失败", logger.ErrorField(err))
		resp.Code = 1
		resp.Message = err.Error()
		return resp, nil
	}

	resp.Code = 0
	resp.Message = "success"
	resp.Total = int32(total)
	for _, tool := range tools {
		protoTool, convErr := convertModelToolToProtoTool(tool)
		if convErr != nil {
			logger.Error("转换工具信息失败",
				logger.StringField("id", tool.ID),
				logger.ErrorField(convErr))
			resp.Code = 1
			resp.Message = convErr.Error()
			resp.Tools = nil
			return resp, nil
		}
		resp.Tools = append(resp.Tools, protoTool)
	}
	return resp, nil
}

func (s *MCPServiceImpl) GetTool(ctx context.Context, req *pb.GetToolRequest) (*pb.GetToolResponse, error) {
	resp := &pb.GetToolResponse{}

	tool, err := s.MCPService.GetTool(ctx, req.ToolId)
	if err != nil {
		logger.Error("获取工具详情失败", logger.ErrorField(err))
		resp.Code = 1
		resp.Message = err.Error()
		return resp, nil
	}
	if tool == nil {
		resp.Code = 1
		resp.Message = "工具不存在"
		return resp, nil
	}

	resp.Code = 0
	resp.Message = "success"
	data, convErr := convertModelToolToProtoTool(tool)
	if convErr != nil {
		logger.Error("转换工具信息失败",
			logger.StringField("id", tool.ID),
			logger.ErrorField(convErr))
		resp.Code = 1
		resp.Message = convErr.Error()
		return resp, nil
	}
	resp.Data = data
	return resp, nil
}

func (s *MCPServiceImpl) UpdateTool(ctx context.Context, req *pb.UpdateToolRequest) (*pb.UpdateToolResponse, error) {
	resp := &pb.UpdateToolResponse{}

	var params []service.ToolParameter
	for _, p := range req.Parameters {
		params = append(params, service.ToolParameter{
			Name:         p.Name,
			Type:         p.Type,
			Description:  p.Description,
			Required:     p.Required,
			DefaultValue: p.DefaultValue,
		})
	}

	tool, err := s.MCPService.UpdateTool(ctx, req.ToolId, req.Name, req.Description, params, req.Config, req.Enabled)
	if err != nil {
		logger.Error("更新工具失败", logger.ErrorField(err))
		resp.Code = 1
		resp.Message = err.Error()
		return resp, nil
	}

	resp.Code = 0
	resp.Message = "success"
	data, convErr := convertModelToolToProtoTool(tool)
	if convErr != nil {
		logger.Error("转换工具信息失败",
			logger.StringField("id", tool.ID),
			logger.ErrorField(convErr))
		resp.Code = 1
		resp.Message = convErr.Error()
		return resp, nil
	}
	resp.Data = data
	return resp, nil
}

func (s *MCPServiceImpl) DeleteTool(ctx context.Context, req *pb.DeleteToolRequest) (*pb.DeleteToolResponse, error) {
	resp := &pb.DeleteToolResponse{}

	if err := s.MCPService.DeleteTool(ctx, req.ToolId); err != nil {
		logger.Error("删除工具失败", logger.ErrorField(err))
		resp.Code = 1
		resp.Message = err.Error()
		return resp, nil
	}

	resp.Code = 0
	resp.Message = "success"
	return resp, nil
}

func convertModelToolToProtoTool(tool *model.Tool) (*pb.Tool, error) {
	if tool == nil {
		return nil, nil
	}

	var config map[string]string
	if tool.Config != "" {
		if err := json.Unmarshal([]byte(tool.Config), &config); err != nil {
			return nil, fmt.Errorf("工具 %s 的配置 JSON 损坏: %w", tool.ID, err)
		}
	}

	return &pb.Tool{
		Id:          tool.ID,
		Name:        tool.Name,
		Description: tool.Description,
		Type:        pb.ToolType(tool.Type),
		Config:      config,
		Enabled:     tool.Enabled,
		CreatedAt:   timestamppb.New(tool.CreatedAt),
		UpdatedAt:   timestamppb.New(tool.UpdatedAt),
	}, nil
}

func (s *MCPServiceImpl) CreateMCPService(ctx context.Context, req *pb.CreateMCPServiceRequest) (*pb.CreateMCPServiceResponse, error) {
	resp := &pb.CreateMCPServiceResponse{}

	svc, err := s.MCPServiceSvc.CreateService(ctx, req.Name, req.Description, req.TransportType, req.Url, req.Headers, req.AuthConfig, req.AdvancedConfig, req.Enabled, getUserIDFromContext(ctx))
	if err != nil {
		logger.Error("创建MCP服务失败", logger.ErrorField(err))
		resp.Code = 1
		resp.Message = err.Error()
		return resp, nil
	}

	resp.Code = 0
	resp.Message = "success"
	data, convErr := convertModelMCPServiceToProto(svc)
	if convErr != nil {
		logger.Error("转换MCP服务信息失败",
			logger.StringField("id", svc.ID),
			logger.ErrorField(convErr))
		resp.Code = 1
		resp.Message = convErr.Error()
		return resp, nil
	}
	resp.Data = data
	return resp, nil
}

func (s *MCPServiceImpl) GetMCPService(ctx context.Context, req *pb.GetMCPServiceRequest) (*pb.GetMCPServiceResponse, error) {
	resp := &pb.GetMCPServiceResponse{}

	svc, err := s.MCPServiceSvc.GetService(ctx, req.ServiceId)
	if err != nil {
		logger.Error("获取MCP服务失败", logger.ErrorField(err))
		resp.Code = 1
		resp.Message = err.Error()
		return resp, nil
	}
	if svc == nil {
		resp.Code = 1
		resp.Message = "MCP服务不存在"
		return resp, nil
	}

	resp.Code = 0
	resp.Message = "success"
	data, convErr := convertModelMCPServiceToProto(svc)
	if convErr != nil {
		logger.Error("转换MCP服务信息失败",
			logger.StringField("id", svc.ID),
			logger.ErrorField(convErr))
		resp.Code = 1
		resp.Message = convErr.Error()
		return resp, nil
	}
	resp.Data = data
	return resp, nil
}

func (s *MCPServiceImpl) ListMCPServices(ctx context.Context, req *pb.ListMCPServicesRequest) (*pb.ListMCPServicesResponse, error) {
	resp := &pb.ListMCPServicesResponse{}

	page, pageSize := pagination.Normalize(int(req.Page), int(req.PageSize))

	services, total, err := s.MCPServiceSvc.ListServices(ctx, req.EnabledOnly, getUserIDFromContext(ctx), page, pageSize)
	if err != nil {
		logger.Error("获取MCP服务列表失败", logger.ErrorField(err))
		resp.Code = 1
		resp.Message = err.Error()
		return resp, nil
	}

	resp.Code = 0
	resp.Message = "success"
	resp.Total = int32(total)
	for _, svc := range services {
		protoSvc, convErr := convertModelMCPServiceToProto(svc)
		if convErr != nil {
			logger.Error("转换MCP服务信息失败",
				logger.StringField("id", svc.ID),
				logger.ErrorField(convErr))
			resp.Code = 1
			resp.Message = convErr.Error()
			resp.Services = nil
			return resp, nil
		}
		resp.Services = append(resp.Services, protoSvc)
	}
	return resp, nil
}

func (s *MCPServiceImpl) UpdateMCPService(ctx context.Context, req *pb.UpdateMCPServiceRequest) (*pb.UpdateMCPServiceResponse, error) {
	resp := &pb.UpdateMCPServiceResponse{}

	svc, err := s.MCPServiceSvc.UpdateService(ctx, req.ServiceId, req.Name, req.Description, req.TransportType, req.Url, req.Headers, req.AuthConfig, req.AdvancedConfig, req.Enabled)
	if err != nil {
		logger.Error("更新MCP服务失败", logger.ErrorField(err))
		resp.Code = 1
		resp.Message = err.Error()
		return resp, nil
	}

	resp.Code = 0
	resp.Message = "success"
	data, convErr := convertModelMCPServiceToProto(svc)
	if convErr != nil {
		logger.Error("转换MCP服务信息失败",
			logger.StringField("id", svc.ID),
			logger.ErrorField(convErr))
		resp.Code = 1
		resp.Message = convErr.Error()
		return resp, nil
	}
	resp.Data = data
	return resp, nil
}

func (s *MCPServiceImpl) DeleteMCPService(ctx context.Context, req *pb.DeleteMCPServiceRequest) (*pb.DeleteMCPServiceResponse, error) {
	resp := &pb.DeleteMCPServiceResponse{}

	if err := s.MCPServiceSvc.DeleteService(ctx, req.ServiceId); err != nil {
		logger.Error("删除MCP服务失败", logger.ErrorField(err))
		resp.Code = 1
		resp.Message = err.Error()
		return resp, nil
	}

	resp.Code = 0
	resp.Message = "success"
	return resp, nil
}

func (s *MCPServiceImpl) TestMCPService(ctx context.Context, req *pb.TestMCPServiceRequest) (*pb.TestMCPServiceResponse, error) {
	resp := &pb.TestMCPServiceResponse{}

	if err := s.MCPServiceSvc.TestConnection(ctx, req.Url, req.TransportType, req.Headers, req.AuthConfig); err != nil {
		logger.Error("测试MCP服务连接失败", logger.ErrorField(err))
		resp.Code = 1
		resp.Message = err.Error()
		return resp, nil
	}

	resp.Code = 0
	resp.Message = "连接成功"
	return resp, nil
}

func convertModelMCPServiceToProto(svc *model.MCPService) (*pb.MCPServiceInfo, error) {
	if svc == nil {
		return nil, nil
	}

	headers, err := decodeMCPServiceMap(svc.ID, "headers", svc.Headers)
	if err != nil {
		return nil, err
	}
	authConfig, err := decodeMCPServiceMap(svc.ID, "auth_config", svc.AuthConfig)
	if err != nil {
		return nil, err
	}
	advancedConfig, err := decodeMCPServiceMap(svc.ID, "advanced_config", svc.AdvancedConfig)
	if err != nil {
		return nil, err
	}

	return &pb.MCPServiceInfo{
		Id:             svc.ID,
		Name:           svc.Name,
		Description:    svc.Description,
		Enabled:        svc.Enabled,
		TransportType:  svc.TransportType,
		Url:            svc.URL,
		Headers:        headers,
		AuthConfig:     authConfig,
		AdvancedConfig: advancedConfig,
		CreatedAt:      timestamppb.New(svc.CreatedAt),
		UpdatedAt:      timestamppb.New(svc.UpdatedAt),
	}, nil
}

func decodeMCPServiceMap(serviceID, field, raw string) (map[string]string, error) {
	if raw == "" {
		return nil, nil
	}
	var result map[string]string
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return nil, fmt.Errorf("MCP服务 %s 的 %s JSON 损坏: %w", serviceID, field, err)
	}
	return result, nil
}
