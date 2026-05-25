package handler

import (
	"context"
	"encoding/json"

	"Logos/internal/service/ai/mcp/model"
	"Logos/internal/service/ai/mcp/service"
	"Logos/pkg/logger"
	pb "Logos/proto_gen/mcp"

	"google.golang.org/protobuf/types/known/timestamppb"
)

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

	tool, err := s.MCPService.RegisterTool(ctx, req.Name, req.Description, int(req.Type), params, req.Config)
	if err != nil {
		logger.Error("注册工具失败", logger.ErrorField(err))
		resp.Code = 1
		resp.Message = err.Error()
		return resp, nil
	}

	resp.Code = 0
	resp.Message = "success"
	resp.Data = convertModelToolToProtoTool(tool)
	return resp, nil
}

func (s *MCPServiceImpl) ListTools(ctx context.Context, req *pb.ListToolsRequest) (*pb.ListToolsResponse, error) {
	resp := &pb.ListToolsResponse{}

	var toolType *int
	if req.Type != 0 {
		t := int(req.Type)
		toolType = &t
	}

	page := int(req.Page)
	if page <= 0 {
		page = 1
	}
	pageSize := int(req.PageSize)
	if pageSize <= 0 {
		pageSize = 20
	}

	tools, total, err := s.MCPService.ListTools(ctx, toolType, req.EnabledOnly, page, pageSize)
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
		resp.Tools = append(resp.Tools, convertModelToolToProtoTool(tool))
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
	resp.Data = convertModelToolToProtoTool(tool)
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
	resp.Data = convertModelToolToProtoTool(tool)
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

func convertModelToolToProtoTool(tool *model.Tool) *pb.Tool {
	if tool == nil {
		return nil
	}

	var config map[string]string
	if tool.Config != "" {
		json.Unmarshal([]byte(tool.Config), &config)
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
	}
}

func (s *MCPServiceImpl) CreateMCPService(ctx context.Context, req *pb.CreateMCPServiceRequest) (*pb.CreateMCPServiceResponse, error) {
	resp := &pb.CreateMCPServiceResponse{}

	svc, err := s.MCPServiceSvc.CreateService(ctx, req.Name, req.Description, req.TransportType, req.Url, req.Headers, req.AuthConfig, req.AdvancedConfig, req.Enabled)
	if err != nil {
		logger.Error("创建MCP服务失败", logger.ErrorField(err))
		resp.Code = 1
		resp.Message = err.Error()
		return resp, nil
	}

	resp.Code = 0
	resp.Message = "success"
	resp.Data = convertModelMCPServiceToProto(svc)
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
	resp.Data = convertModelMCPServiceToProto(svc)
	return resp, nil
}

func (s *MCPServiceImpl) ListMCPServices(ctx context.Context, req *pb.ListMCPServicesRequest) (*pb.ListMCPServicesResponse, error) {
	resp := &pb.ListMCPServicesResponse{}

	page := int(req.Page)
	if page <= 0 {
		page = 1
	}
	pageSize := int(req.PageSize)
	if pageSize <= 0 {
		pageSize = 20
	}

	services, total, err := s.MCPServiceSvc.ListServices(ctx, req.EnabledOnly, page, pageSize)
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
		resp.Services = append(resp.Services, convertModelMCPServiceToProto(svc))
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
	resp.Data = convertModelMCPServiceToProto(svc)
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

func convertModelMCPServiceToProto(svc *model.MCPService) *pb.MCPServiceInfo {
	if svc == nil {
		return nil
	}

	var headers map[string]string
	if svc.Headers != "" {
		json.Unmarshal([]byte(svc.Headers), &headers)
	}

	var authConfig map[string]string
	if svc.AuthConfig != "" {
		json.Unmarshal([]byte(svc.AuthConfig), &authConfig)
	}

	var advancedConfig map[string]string
	if svc.AdvancedConfig != "" {
		json.Unmarshal([]byte(svc.AdvancedConfig), &advancedConfig)
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
	}
}
