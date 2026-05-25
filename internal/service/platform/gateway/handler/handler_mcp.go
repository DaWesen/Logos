package handler

import (
	"context"
	"net/http"

	"Logos/internal/service/platform/gateway/model"
	pb "Logos/proto_gen/mcp"

	"github.com/gin-gonic/gin"
)

func (h *Handler) CallTool(c *gin.Context) {
	if h.MCPClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "MCP服务不可用"))
		return
	}
	var req pb.CallToolRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.MCPClient.CallTool(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	c.JSON(toHTTPCode(resp.Code), model.Response{Code: int(resp.Code), Message: resp.Message, Data: gin.H{
		"result":   resp.Result,
		"metadata": resp.Metadata,
	}})
}

func (h *Handler) RegisterTool(c *gin.Context) {
	if h.MCPClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "MCP服务不可用"))
		return
	}
	var req pb.RegisterToolRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.MCPClient.RegisterTool(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	c.JSON(toHTTPCode(resp.Code), model.Response{Code: int(resp.Code), Message: resp.Message, Data: resp.Data})
}

func (h *Handler) ListMCPTools(c *gin.Context) {
	if h.MCPClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "MCP服务不可用"))
		return
	}
	var req pb.ListToolsRequest
	c.ShouldBindJSON(&req)
	resp, err := h.MCPClient.ListTools(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	c.JSON(toHTTPCode(resp.Code), model.Response{Code: int(resp.Code), Message: resp.Message, Data: gin.H{
		"tools": resp.Tools,
		"total": resp.Total,
	}})
}

func (h *Handler) GetMCPTool(c *gin.Context) {
	toolID := c.Param("id")
	if h.MCPClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "MCP服务不可用"))
		return
	}
	resp, err := h.MCPClient.GetTool(context.Background(), &pb.GetToolRequest{ToolId: toolID})
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	c.JSON(toHTTPCode(resp.Code), model.Response{Code: int(resp.Code), Message: resp.Message, Data: resp.Data})
}

func (h *Handler) UpdateMCPTool(c *gin.Context) {
	toolID := c.Param("id")
	if h.MCPClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "MCP服务不可用"))
		return
	}
	var req pb.UpdateToolRequest
	req.ToolId = toolID
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.MCPClient.UpdateTool(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	c.JSON(toHTTPCode(resp.Code), model.Response{Code: int(resp.Code), Message: resp.Message, Data: resp.Data})
}

func (h *Handler) DeleteMCPTool(c *gin.Context) {
	toolID := c.Param("id")
	if h.MCPClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "MCP服务不可用"))
		return
	}
	resp, err := h.MCPClient.DeleteTool(context.Background(), &pb.DeleteToolRequest{ToolId: toolID})
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	c.JSON(toHTTPCode(resp.Code), model.Response{Code: int(resp.Code), Message: resp.Message, Data: gin.H{
		"success": resp.Code == 0,
		"message": resp.Message,
	}})
}

func (h *Handler) CreateMCPService(c *gin.Context) {
	if h.MCPClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "MCP服务不可用"))
		return
	}
	var req pb.CreateMCPServiceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.MCPClient.CreateMCPService(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	c.JSON(toHTTPCode(resp.Code), model.Response{Code: int(resp.Code), Message: resp.Message, Data: resp.Data})
}

func (h *Handler) ListMCPServices(c *gin.Context) {
	if h.MCPClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "MCP服务不可用"))
		return
	}
	var req pb.ListMCPServicesRequest
	c.ShouldBindJSON(&req)
	if req.PageSize == 0 {
		req.PageSize = 100
	}
	resp, err := h.MCPClient.ListMCPServices(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	c.JSON(toHTTPCode(resp.Code), model.Response{Code: int(resp.Code), Message: resp.Message, Data: gin.H{
		"services": resp.Services,
		"total":    resp.Total,
	}})
}

func (h *Handler) GetMCPService(c *gin.Context) {
	serviceID := c.Param("id")
	if h.MCPClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "MCP服务不可用"))
		return
	}
	resp, err := h.MCPClient.GetMCPService(context.Background(), &pb.GetMCPServiceRequest{ServiceId: serviceID})
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	c.JSON(toHTTPCode(resp.Code), model.Response{Code: int(resp.Code), Message: resp.Message, Data: resp.Data})
}

func (h *Handler) UpdateMCPService(c *gin.Context) {
	serviceID := c.Param("id")
	if h.MCPClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "MCP服务不可用"))
		return
	}
	var req pb.UpdateMCPServiceRequest
	req.ServiceId = serviceID
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.MCPClient.UpdateMCPService(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	c.JSON(toHTTPCode(resp.Code), model.Response{Code: int(resp.Code), Message: resp.Message, Data: resp.Data})
}

func (h *Handler) DeleteMCPService(c *gin.Context) {
	serviceID := c.Param("id")
	if h.MCPClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "MCP服务不可用"))
		return
	}
	resp, err := h.MCPClient.DeleteMCPService(context.Background(), &pb.DeleteMCPServiceRequest{ServiceId: serviceID})
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	c.JSON(toHTTPCode(resp.Code), model.Response{Code: int(resp.Code), Message: resp.Message})
}

func (h *Handler) TestMCPService(c *gin.Context) {
	if h.MCPClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "MCP服务不可用"))
		return
	}
	var req pb.TestMCPServiceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.MCPClient.TestMCPService(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	c.JSON(toHTTPCode(resp.Code), model.Response{Code: int(resp.Code), Message: resp.Message, Data: gin.H{
		"success": resp.Code == 0,
		"message": resp.Message,
	}})
}
