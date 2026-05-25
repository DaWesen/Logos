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
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "operation"))
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
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "operation"))
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
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "operation"))
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
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "operation"))
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSizeStr := c.DefaultQuery("pageSize", c.DefaultQuery("page_size", "20"))
	pageSize, _ := strconv.Atoi(pageSizeStr)

	req := &pb.QueryEntityReq{
		Page:         int32(page),
		PageSize:     int32(pageSize),
		CollectionId: c.Query("collectionId"),
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
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "operation"))
		return
	}
	keyword := c.Query("keyword")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSizeStr := c.DefaultQuery("pageSize", c.DefaultQuery("page_size", "20"))
	pageSize, _ := strconv.Atoi(pageSizeStr)

	req := &pb.SearchEntityReq{
		Keyword:      keyword,
		Page:         int32(page),
		PageSize:     int32(pageSize),
		CollectionId: c.Query("collectionId"),
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
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "operation"))
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
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "operation"))
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
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "operation"))
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
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "operation"))
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
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "operation"))
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSizeStr := c.DefaultQuery("pageSize", c.DefaultQuery("page_size", "20"))
	pageSize, _ := strconv.Atoi(pageSizeStr)

	req := &pb.QueryRelationReq{
		Page:         int32(page),
		PageSize:     int32(pageSize),
		CollectionId: c.Query("collectionId"),
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
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "operation"))
		return
	}
	req := &pb.EmptyReq{
		CollectionId: c.Query("collectionId"),
	}
	resp, err := h.KnowledgeClient.GetGraphStats(context.Background(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp.BaseResp != nil {
		statusCode = int(resp.BaseResp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: gin.H{
		"entity_count":        resp.EntityCount,
		"relation_count":      resp.RelationCount,
		"entity_type_count":   resp.EntityTypeCount,
		"relation_type_count": resp.RelationTypeCount,
	}})
}

func (h *Handler) GetRelatedEntities(c *gin.Context) {
	if h.KnowledgeClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "operation"))
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
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "operation"))
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

func (h *Handler) GetSubgraph(c *gin.Context) {
	if h.KnowledgeClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "operation"))
		return
	}
	entityID := c.Param("id")
	depth, _ := strconv.Atoi(c.DefaultQuery("depth", "2"))
	collectionID := c.Query("collectionId")

	req := &pb.GetSubgraphReq{
		EntityId:     entityID,
		Depth:        int32(depth),
		CollectionId: collectionID,
	}

	resp, err := h.KnowledgeClient.GetSubgraph(context.Background(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp.BaseResp != nil {
		statusCode = int(resp.BaseResp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: gin.H{
		"nodes":     resp.Nodes,
		"edges":     resp.Edges,
		"nodeCount": resp.NodeCount,
		"edgeCount": resp.EdgeCount,
	}})
}

func (h *Handler) ClearEntities(c *gin.Context) {
	if h.KnowledgeClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "operation"))
		return
	}

	collectionID := c.Query("collectionId")

	page := int32(1)
	pageSize := int32(200)
	var deletedCount int

	for {
		req := &pb.QueryEntityReq{
			Page:         page,
			PageSize:     pageSize,
			CollectionId: collectionID,
		}

		resp, err := h.KnowledgeClient.QueryEntities(context.Background(), req)
		if err != nil {
			c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
			return
		}
		if resp.BaseResp != nil && resp.BaseResp.StatusCode != 0 {
			c.JSON(int(resp.BaseResp.StatusCode), model.Error(int(resp.BaseResp.StatusCode), resp.BaseResp.StatusMessage))
			return
		}

		if len(resp.Entities) == 0 {
			break
		}

		for _, entity := range resp.Entities {
			delResp, delErr := h.KnowledgeClient.DeleteEntity(context.Background(), &pb.DeleteEntityReq{Id: entity.Id})
			if delErr != nil {
				continue
			}
			if delResp != nil && delResp.StatusCode == 0 {
				deletedCount++
			}
		}

		if int32(len(resp.Entities)) < pageSize {
			break
		}
	}

	c.JSON(http.StatusOK, model.Success(gin.H{"deleted_count": deletedCount}))
}

func (h *Handler) GetEntityPaths(c *gin.Context) {
	if h.KnowledgeClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "operation"))
		return
	}
	sourceID := c.Query("sourceId")
	targetID := c.Query("targetId")
	maxDepth, _ := strconv.Atoi(c.DefaultQuery("maxDepth", "4"))
	collectionID := c.Query("collectionId")

	if sourceID == "" || targetID == "" {
		c.JSON(http.StatusBadRequest, model.BadRequest("sourceId and targetId are required"))
		return
	}

	req := &pb.GetEntityPathsReq{
		SourceId:     sourceID,
		TargetId:     targetID,
		MaxDepth:     int32(maxDepth),
		CollectionId: collectionID,
	}

	resp, err := h.KnowledgeClient.GetEntityPaths(context.Background(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp.BaseResp != nil {
		statusCode = int(resp.BaseResp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: resp.Paths})
}
