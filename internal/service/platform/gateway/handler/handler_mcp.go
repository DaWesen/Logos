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
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
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
	c.JSON(toHTTPCode(resp.Code), model.Response{Code: int(resp.Code), Message: resp.Message})
}
