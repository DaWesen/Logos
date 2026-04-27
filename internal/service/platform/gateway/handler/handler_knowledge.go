package handler

import (
	"context"
	"net/http"
	"strconv"

	"Logos/internal/service/platform/gateway/model"

	pb "Logos/proto_gen/knowledge"

	"github.com/gin-gonic/gin"
)

func (h *Handler) AddEntity(c *gin.Context) {
	if h.KnowledgeClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "internal server error"))
		return
	}
	var req pb.AddEntityReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.KnowledgeClient.AddEntity(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp.BaseResp != nil {
		statusCode = int(resp.BaseResp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: resp.Entity})
}

func (h *Handler) UpdateEntity(c *gin.Context) {
	if h.KnowledgeClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "֪ʶ�����ݲ�����"))
		return
	}
	var req pb.UpdateEntityReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.KnowledgeClient.UpdateEntity(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp.BaseResp != nil {
		statusCode = int(resp.BaseResp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: resp.Entity})
}

func (h *Handler) DeleteEntity(c *gin.Context) {
	if h.KnowledgeClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "֪ʶ�����ݲ�����"))
		return
	}
	id := c.Param("id")
	req := &pb.DeleteEntityReq{Id: id}
	resp, err := h.KnowledgeClient.DeleteEntity(context.Background(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp != nil {
		statusCode = int(resp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp), Data: nil})
}

func (h *Handler) GetEntity(c *gin.Context) {
	if h.KnowledgeClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "֪ʶ�����ݲ�����"))
		return
	}
	id := c.Param("id")
	resp, err := h.KnowledgeClient.GetEntity(context.Background(), &pb.GetByIdReq{Id: id})
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp.BaseResp != nil {
		statusCode = int(resp.BaseResp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: resp.Entity})
}

func (h *Handler) QueryEntities(c *gin.Context) {
	if h.KnowledgeClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "֪ʶ�����ݲ�����"))
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("pageSize", "20"))

	req := &pb.QueryEntityReq{
		Page:     int32(page),
		PageSize: int32(pageSize),
	}
	if entityType := c.Query("type"); entityType != "" {
		req.Type = &entityType
	}
	if name := c.Query("name"); name != "" {
		req.Name = &name
	}

	resp, err := h.KnowledgeClient.QueryEntities(context.Background(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp.BaseResp != nil {
		statusCode = int(resp.BaseResp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: resp.Entities})
}

func (h *Handler) SearchEntities(c *gin.Context) {
	if h.KnowledgeClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "֪ʶ�����ݲ�����"))
		return
	}
	keyword := c.Query("keyword")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("pageSize", "20"))

	req := &pb.SearchEntityReq{
		Keyword:  keyword,
		Page:     int32(page),
		PageSize: int32(pageSize),
	}
	if entityType := c.Query("type"); entityType != "" {
		req.Type = &entityType
	}

	resp, err := h.KnowledgeClient.SearchEntities(context.Background(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp.BaseResp != nil {
		statusCode = int(resp.BaseResp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: resp.Entities})
}

func (h *Handler) AddRelation(c *gin.Context) {
	if h.KnowledgeClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "֪ʶ�����ݲ�����"))
		return
	}
	var req pb.AddRelationReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.KnowledgeClient.AddRelation(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp.BaseResp != nil {
		statusCode = int(resp.BaseResp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: resp.Relation})
}

func (h *Handler) UpdateRelation(c *gin.Context) {
	if h.KnowledgeClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "֪ʶ�����ݲ�����"))
		return
	}
	var req pb.UpdateRelationReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.KnowledgeClient.UpdateRelation(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp.BaseResp != nil {
		statusCode = int(resp.BaseResp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: resp.Relation})
}

func (h *Handler) DeleteRelation(c *gin.Context) {
	if h.KnowledgeClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "֪ʶ�����ݲ�����"))
		return
	}
	id := c.Param("id")
	req := &pb.DeleteRelationReq{Id: id}
	resp, err := h.KnowledgeClient.DeleteRelation(context.Background(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp != nil {
		statusCode = int(resp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp), Data: nil})
}

func (h *Handler) GetRelation(c *gin.Context) {
	if h.KnowledgeClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "֪ʶ�����ݲ�����"))
		return
	}
	id := c.Param("id")
	resp, err := h.KnowledgeClient.GetRelation(context.Background(), &pb.GetByIdReq{Id: id})
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp.BaseResp != nil {
		statusCode = int(resp.BaseResp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: resp.Relation})
}

func (h *Handler) QueryRelations(c *gin.Context) {
	if h.KnowledgeClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "֪ʶ�����ݲ�����"))
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("pageSize", "20"))

	req := &pb.QueryRelationReq{
		Page:     int32(page),
		PageSize: int32(pageSize),
	}
	if relType := c.Query("type"); relType != "" {
		req.Type = &relType
	}
	if sourceId := c.Query("sourceId"); sourceId != "" {
		req.SourceId = &sourceId
	}
	if targetId := c.Query("targetId"); targetId != "" {
		req.TargetId = &targetId
	}

	resp, err := h.KnowledgeClient.QueryRelations(context.Background(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp.BaseResp != nil {
		statusCode = int(resp.BaseResp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: resp.Relations})
}

func (h *Handler) GetGraphStats(c *gin.Context) {
	if h.KnowledgeClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "֪ʶ�����ݲ�����"))
		return
	}
	resp, err := h.KnowledgeClient.GetGraphStats(context.Background(), &pb.EmptyReq{})
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp.BaseResp != nil {
		statusCode = int(resp.BaseResp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: gin.H{
		"entityCount":       resp.EntityCount,
		"relationCount":     resp.RelationCount,
		"entityTypeCount":   resp.EntityTypeCount,
		"relationTypeCount": resp.RelationTypeCount,
	}})
}

func (h *Handler) GetRelatedEntities(c *gin.Context) {
	if h.KnowledgeClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "֪ʶ�����ݲ�����"))
		return
	}
	entityID := c.Param("entityId")
	relationType := c.Query("relationType")

	req := &pb.GetRelatedEntitiesReq{EntityId: entityID}
	if relationType != "" {
		req.RelationType = &relationType
	}

	resp, err := h.KnowledgeClient.GetRelatedEntities(context.Background(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp.BaseResp != nil {
		statusCode = int(resp.BaseResp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: resp.Entities})
}

func (h *Handler) ImportData(c *gin.Context) {
	if h.KnowledgeClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "֪ʶ�����ݲ�����"))
		return
	}
	var req pb.ImportDataReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.KnowledgeClient.ImportData(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp != nil {
		statusCode = int(resp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp), Data: nil})
}
