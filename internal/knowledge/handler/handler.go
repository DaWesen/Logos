package handler

import (
	"Noah/internal/knowledge/model"
	"Noah/internal/knowledge/service"
	common "Noah/kitex_gen/common"
	knowledge "Noah/kitex_gen/knowledge"
	"Noah/pkg/logger"
	"context"
	"time"
)

// KnowledgeServiceImpl implements the last service interface defined in the IDL.
type KnowledgeServiceImpl struct {
	KnowledgeService service.KnowledgeService
}

func convertModelEntityToThriftEntity(me *model.Entity) *knowledge.Entity {
	if me == nil {
		return nil
	}
	te := &knowledge.Entity{
		Id:         me.ID,
		Type:       me.Type,
		Name:       me.Name,
		Properties: map[string]string(me.Properties),
		CreatedAt:  me.CreatedAt.Unix(),
		UpdatedAt:  me.UpdatedAt.Unix(),
	}
	if me.Description != nil {
		te.Description = me.Description
	}
	return te
}

func convertModelRelationToThriftRelation(mr *model.Relation) *knowledge.Relation {
	if mr == nil {
		return nil
	}
	tr := &knowledge.Relation{
		Id:         mr.ID,
		Type:       mr.Type,
		SourceId:   mr.SourceID,
		TargetId:   mr.TargetID,
		Properties: map[string]string(mr.Properties),
		CreatedAt:  mr.CreatedAt.Unix(),
		UpdatedAt:  mr.UpdatedAt.Unix(),
	}
	if mr.Description != nil {
		tr.Description = mr.Description
	}
	return tr
}

func buildSuccessBaseResp() *common.BaseResp {
	return &common.BaseResp{
		StatusCode:    0,
		StatusMessage: "success",
		ServiceTime:   time.Now().Unix(),
	}
}

func buildErrorBaseResp(message string) *common.BaseResp {
	return &common.BaseResp{
		StatusCode:    1,
		StatusMessage: message,
		ServiceTime:   time.Now().Unix(),
	}
}

// AddEntity implements the KnowledgeServiceImpl interface.
func (s *KnowledgeServiceImpl) AddEntity(ctx context.Context, req *knowledge.AddEntityReq) (resp *knowledge.EntityResp, err error) {
	resp = knowledge.NewEntityResp()

	entity, err := s.KnowledgeService.AddEntity(ctx, req.Type, req.Name, req.Properties, req.Description)
	if err != nil {
		logger.Error("添加实体失败", logger.ErrorField(err))
		resp.BaseResp = buildErrorBaseResp(err.Error())
		return resp, nil
	}

	resp.BaseResp = buildSuccessBaseResp()
	resp.Entity = convertModelEntityToThriftEntity(entity)
	return resp, nil
}

// UpdateEntity implements the KnowledgeServiceImpl interface.
func (s *KnowledgeServiceImpl) UpdateEntity(ctx context.Context, req *knowledge.UpdateEntityReq) (resp *knowledge.EntityResp, err error) {
	resp = knowledge.NewEntityResp()

	entityType := ""
	if req.IsSetType() {
		entityType = *req.Type
	}
	name := ""
	if req.IsSetName() {
		name = *req.Name
	}
	var properties map[string]string
	if req.IsSetProperties() {
		properties = req.Properties
	}
	var description *string
	if req.IsSetDescription() {
		description = req.Description
	}

	entity, err := s.KnowledgeService.UpdateEntity(ctx, req.Id, entityType, name, properties, description)
	if err != nil {
		logger.Error("更新实体失败", logger.ErrorField(err))
		resp.BaseResp = buildErrorBaseResp(err.Error())
		return resp, nil
	}

	resp.BaseResp = buildSuccessBaseResp()
	resp.Entity = convertModelEntityToThriftEntity(entity)
	return resp, nil
}

// DeleteEntity implements the KnowledgeServiceImpl interface.
func (s *KnowledgeServiceImpl) DeleteEntity(ctx context.Context, req *knowledge.DeleteEntityReq) (resp *common.BaseResp, err error) {
	err = s.KnowledgeService.DeleteEntity(ctx, req.Id)
	if err != nil {
		logger.Error("删除实体失败", logger.ErrorField(err))
		return buildErrorBaseResp(err.Error()), nil
	}
	return buildSuccessBaseResp(), nil
}

// GetEntity implements the KnowledgeServiceImpl interface.
func (s *KnowledgeServiceImpl) GetEntity(ctx context.Context, id string) (resp *knowledge.EntityResp, err error) {
	resp = knowledge.NewEntityResp()

	entity, err := s.KnowledgeService.GetEntity(ctx, id)
	if err != nil {
		logger.Error("获取实体失败", logger.ErrorField(err))
		resp.BaseResp = buildErrorBaseResp(err.Error())
		return resp, nil
	}

	resp.BaseResp = buildSuccessBaseResp()
	resp.Entity = convertModelEntityToThriftEntity(entity)
	return resp, nil
}

// QueryEntities implements the KnowledgeServiceImpl interface.
func (s *KnowledgeServiceImpl) QueryEntities(ctx context.Context, req *knowledge.QueryEntityReq) (resp *knowledge.BatchEntityResp, err error) {
	resp = knowledge.NewBatchEntityResp()

	entityType := ""
	if req.IsSetType() {
		entityType = *req.Type
	}
	name := ""
	if req.IsSetName() {
		name = *req.Name
	}
	var properties map[string]string
	if req.IsSetProperties() {
		properties = req.Properties
	}

	entities, _, err := s.KnowledgeService.QueryEntities(ctx, entityType, name, properties, int(req.Page), int(req.PageSize))
	if err != nil {
		logger.Error("查询实体失败", logger.ErrorField(err))
		resp.BaseResp = buildErrorBaseResp(err.Error())
		return resp, nil
	}

	resp.BaseResp = buildSuccessBaseResp()
	resp.Entities = make([]*knowledge.Entity, 0, len(entities))
	for _, e := range entities {
		resp.Entities = append(resp.Entities, convertModelEntityToThriftEntity(e))
	}
	return resp, nil
}

// AddRelation implements the KnowledgeServiceImpl interface.
func (s *KnowledgeServiceImpl) AddRelation(ctx context.Context, req *knowledge.AddRelationReq) (resp *knowledge.RelationResp, err error) {
	resp = knowledge.NewRelationResp()

	relation, err := s.KnowledgeService.AddRelation(ctx, req.Type, req.SourceId, req.TargetId, req.Properties, req.Description)
	if err != nil {
		logger.Error("添加关系失败", logger.ErrorField(err))
		resp.BaseResp = buildErrorBaseResp(err.Error())
		return resp, nil
	}

	resp.BaseResp = buildSuccessBaseResp()
	resp.Relation = convertModelRelationToThriftRelation(relation)
	return resp, nil
}

// UpdateRelation implements the KnowledgeServiceImpl interface.
func (s *KnowledgeServiceImpl) UpdateRelation(ctx context.Context, req *knowledge.UpdateRelationReq) (resp *knowledge.RelationResp, err error) {
	resp = knowledge.NewRelationResp()

	relationType := ""
	if req.IsSetType() {
		relationType = *req.Type
	}
	sourceId := ""
	if req.IsSetSourceId() {
		sourceId = *req.SourceId
	}
	targetId := ""
	if req.IsSetTargetId() {
		targetId = *req.TargetId
	}
	var properties map[string]string
	if req.IsSetProperties() {
		properties = req.Properties
	}
	var description *string
	if req.IsSetDescription() {
		description = req.Description
	}

	relation, err := s.KnowledgeService.UpdateRelation(ctx, req.Id, relationType, sourceId, targetId, properties, description)
	if err != nil {
		logger.Error("更新关系失败", logger.ErrorField(err))
		resp.BaseResp = buildErrorBaseResp(err.Error())
		return resp, nil
	}

	resp.BaseResp = buildSuccessBaseResp()
	resp.Relation = convertModelRelationToThriftRelation(relation)
	return resp, nil
}

// DeleteRelation implements the KnowledgeServiceImpl interface.
func (s *KnowledgeServiceImpl) DeleteRelation(ctx context.Context, req *knowledge.DeleteRelationReq) (resp *common.BaseResp, err error) {
	err = s.KnowledgeService.DeleteRelation(ctx, req.Id)
	if err != nil {
		logger.Error("删除关系失败", logger.ErrorField(err))
		return buildErrorBaseResp(err.Error()), nil
	}
	return buildSuccessBaseResp(), nil
}

// GetRelation implements the KnowledgeServiceImpl interface.
func (s *KnowledgeServiceImpl) GetRelation(ctx context.Context, id string) (resp *knowledge.RelationResp, err error) {
	resp = knowledge.NewRelationResp()

	relation, err := s.KnowledgeService.GetRelation(ctx, id)
	if err != nil {
		logger.Error("获取关系失败", logger.ErrorField(err))
		resp.BaseResp = buildErrorBaseResp(err.Error())
		return resp, nil
	}

	resp.BaseResp = buildSuccessBaseResp()
	resp.Relation = convertModelRelationToThriftRelation(relation)
	return resp, nil
}

// QueryRelations implements the KnowledgeServiceImpl interface.
func (s *KnowledgeServiceImpl) QueryRelations(ctx context.Context, req *knowledge.QueryRelationReq) (resp *knowledge.BatchRelationResp, err error) {
	resp = knowledge.NewBatchRelationResp()

	relationType := ""
	if req.IsSetType() {
		relationType = *req.Type
	}
	sourceId := ""
	if req.IsSetSourceId() {
		sourceId = *req.SourceId
	}
	targetId := ""
	if req.IsSetTargetId() {
		targetId = *req.TargetId
	}

	relations, _, err := s.KnowledgeService.QueryRelations(ctx, relationType, sourceId, targetId, int(req.Page), int(req.PageSize))
	if err != nil {
		logger.Error("查询关系失败", logger.ErrorField(err))
		resp.BaseResp = buildErrorBaseResp(err.Error())
		return resp, nil
	}

	resp.BaseResp = buildSuccessBaseResp()
	resp.Relations = make([]*knowledge.Relation, 0, len(relations))
	for _, r := range relations {
		resp.Relations = append(resp.Relations, convertModelRelationToThriftRelation(r))
	}
	return resp, nil
}

// GetGraphStats implements the KnowledgeServiceImpl interface.
func (s *KnowledgeServiceImpl) GetGraphStats(ctx context.Context) (resp *knowledge.GraphStatsResp, err error) {
	resp = knowledge.NewGraphStatsResp()

	stats, err := s.KnowledgeService.GetGraphStats(ctx)
	if err != nil {
		logger.Error("获取图谱统计失败", logger.ErrorField(err))
		resp.BaseResp = buildErrorBaseResp(err.Error())
		return resp, nil
	}

	resp.BaseResp = buildSuccessBaseResp()
	resp.EntityCount = stats.EntityCount
	resp.RelationCount = stats.RelationCount
	resp.EntityTypeCount = stats.EntityTypeCount
	resp.RelationTypeCount = stats.RelationTypeCount
	return resp, nil
}

// GetRelatedEntities implements the KnowledgeServiceImpl interface.
func (s *KnowledgeServiceImpl) GetRelatedEntities(ctx context.Context, entityId string, relationType string) (resp *knowledge.BatchEntityResp, err error) {
	resp = knowledge.NewBatchEntityResp()

	entities, err := s.KnowledgeService.GetRelatedEntities(ctx, entityId, relationType)
	if err != nil {
		logger.Error("获取相关实体失败", logger.ErrorField(err))
		resp.BaseResp = buildErrorBaseResp(err.Error())
		return resp, nil
	}

	resp.BaseResp = buildSuccessBaseResp()
	resp.Entities = make([]*knowledge.Entity, 0, len(entities))
	for _, e := range entities {
		resp.Entities = append(resp.Entities, convertModelEntityToThriftEntity(e))
	}
	return resp, nil
}

// ImportData implements the KnowledgeServiceImpl interface.
func (s *KnowledgeServiceImpl) ImportData(ctx context.Context, req *knowledge.ImportDataReq) (resp *common.BaseResp, err error) {
	err = s.KnowledgeService.ImportData(ctx, req.DataType, req.Data)
	if err != nil {
		logger.Error("导入数据失败", logger.ErrorField(err))
		return buildErrorBaseResp(err.Error()), nil
	}
	return buildSuccessBaseResp(), nil
}

// SearchEntities implements the KnowledgeServiceImpl interface.
func (s *KnowledgeServiceImpl) SearchEntities(ctx context.Context, req *knowledge.SearchEntityReq) (resp *knowledge.BatchEntityResp, err error) {
	resp = knowledge.NewBatchEntityResp()

	var entityType *string
	if req.IsSetType() {
		entityType = req.Type
	}

	entities, _, err := s.KnowledgeService.SearchEntities(ctx, req.Keyword, entityType, int(req.Page), int(req.PageSize))
	if err != nil {
		logger.Error("搜索实体失败", logger.ErrorField(err))
		resp.BaseResp = buildErrorBaseResp(err.Error())
		return resp, nil
	}

	resp.BaseResp = buildSuccessBaseResp()
	resp.Entities = make([]*knowledge.Entity, 0, len(entities))
	for _, e := range entities {
		resp.Entities = append(resp.Entities, convertModelEntityToThriftEntity(e))
	}
	return resp, nil
}
