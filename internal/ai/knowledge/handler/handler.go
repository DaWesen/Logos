package handler

import (
	"context"
	"time"

	"Logos/internal/ai/knowledge/model"
	"Logos/internal/ai/knowledge/service"
	pb "Logos/proto_gen/knowledge"
	pbCommon "Logos/proto_gen/common"
	"Logos/pkg/logger"
)

// KnowledgeServiceImpl implements the KnowledgeService interface.
type KnowledgeServiceImpl struct {
	pb.UnimplementedKnowledgeServiceServer
	KnowledgeService service.KnowledgeService
}

func convertModelEntityToProtoEntity(me *model.Entity) *pb.Entity {
	if me == nil {
		return nil
	}
	te := &pb.Entity{
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

func convertModelRelationToProtoRelation(mr *model.Relation) *pb.Relation {
	if mr == nil {
		return nil
	}
	tr := &pb.Relation{
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

func buildSuccessBaseResp() *pbCommon.BaseResp {
	return &pbCommon.BaseResp{
		StatusCode:    0,
		StatusMessage: "success",
		ServiceTime:   time.Now().Unix(),
	}
}

func buildErrorBaseResp(message string) *pbCommon.BaseResp {
	return &pbCommon.BaseResp{
		StatusCode:    1,
		StatusMessage: message,
		ServiceTime:   time.Now().Unix(),
	}
}

// AddEntity implements the KnowledgeServiceImpl interface.
func (s *KnowledgeServiceImpl) AddEntity(ctx context.Context, req *pb.AddEntityReq) (*pb.EntityResp, error) {
	resp := &pb.EntityResp{}

	entity, err := s.KnowledgeService.AddEntity(ctx, req.Type, req.Name, req.Properties, req.Description)
	if err != nil {
		logger.Error("添加实体失败", logger.ErrorField(err))
		resp.BaseResp = buildErrorBaseResp(err.Error())
		return resp, nil
	}

	resp.BaseResp = buildSuccessBaseResp()
	resp.Entity = convertModelEntityToProtoEntity(entity)
	return resp, nil
}

// UpdateEntity implements the KnowledgeServiceImpl interface.
func (s *KnowledgeServiceImpl) UpdateEntity(ctx context.Context, req *pb.UpdateEntityReq) (*pb.EntityResp, error) {
	resp := &pb.EntityResp{}

	entityType := ""
	if req.Type != nil {
		entityType = *req.Type
	}
	name := ""
	if req.Name != nil {
		name = *req.Name
	}
	var properties map[string]string
	if req.Properties != nil {
		properties = req.Properties
	}
	var description *string
	if req.Description != nil {
		description = req.Description
	}

	entity, err := s.KnowledgeService.UpdateEntity(ctx, req.Id, entityType, name, properties, description)
	if err != nil {
		logger.Error("更新实体失败", logger.ErrorField(err))
		resp.BaseResp = buildErrorBaseResp(err.Error())
		return resp, nil
	}

	resp.BaseResp = buildSuccessBaseResp()
	resp.Entity = convertModelEntityToProtoEntity(entity)
	return resp, nil
}

// DeleteEntity implements the KnowledgeServiceImpl interface.
func (s *KnowledgeServiceImpl) DeleteEntity(ctx context.Context, req *pb.DeleteEntityReq) (*pbCommon.BaseResp, error) {
	err := s.KnowledgeService.DeleteEntity(ctx, req.Id)
	if err != nil {
		logger.Error("删除实体失败", logger.ErrorField(err))
		return buildErrorBaseResp(err.Error()), nil
	}
	return buildSuccessBaseResp(), nil
}

// GetEntity implements the KnowledgeServiceImpl interface.
func (s *KnowledgeServiceImpl) GetEntity(ctx context.Context, req *pb.GetByIdReq) (*pb.EntityResp, error) {
	resp := &pb.EntityResp{}

	entity, err := s.KnowledgeService.GetEntity(ctx, req.Id)
	if err != nil {
		logger.Error("获取实体失败", logger.ErrorField(err))
		resp.BaseResp = buildErrorBaseResp(err.Error())
		return resp, nil
	}

	resp.BaseResp = buildSuccessBaseResp()
	resp.Entity = convertModelEntityToProtoEntity(entity)
	return resp, nil
}

// QueryEntities implements the KnowledgeServiceImpl interface.
func (s *KnowledgeServiceImpl) QueryEntities(ctx context.Context, req *pb.QueryEntityReq) (*pb.BatchEntityResp, error) {
	resp := &pb.BatchEntityResp{}

	entityType := ""
	if req.Type != nil {
		entityType = *req.Type
	}
	name := ""
	if req.Name != nil {
		name = *req.Name
	}
	var properties map[string]string
	if req.Properties != nil {
		properties = req.Properties
	}

	entities, _, err := s.KnowledgeService.QueryEntities(ctx, entityType, name, properties, int(req.Page), int(req.PageSize))
	if err != nil {
		logger.Error("查询实体失败", logger.ErrorField(err))
		resp.BaseResp = buildErrorBaseResp(err.Error())
		return resp, nil
	}

	resp.BaseResp = buildSuccessBaseResp()
	resp.Entities = make([]*pb.Entity, 0, len(entities))
	for _, e := range entities {
		resp.Entities = append(resp.Entities, convertModelEntityToProtoEntity(e))
	}
	return resp, nil
}

// AddRelation implements the KnowledgeServiceImpl interface.
func (s *KnowledgeServiceImpl) AddRelation(ctx context.Context, req *pb.AddRelationReq) (*pb.RelationResp, error) {
	resp := &pb.RelationResp{}

	relation, err := s.KnowledgeService.AddRelation(ctx, req.Type, req.SourceId, req.TargetId, req.Properties, req.Description)
	if err != nil {
		logger.Error("添加关系失败", logger.ErrorField(err))
		resp.BaseResp = buildErrorBaseResp(err.Error())
		return resp, nil
	}

	resp.BaseResp = buildSuccessBaseResp()
	resp.Relation = convertModelRelationToProtoRelation(relation)
	return resp, nil
}

// UpdateRelation implements the KnowledgeServiceImpl interface.
func (s *KnowledgeServiceImpl) UpdateRelation(ctx context.Context, req *pb.UpdateRelationReq) (*pb.RelationResp, error) {
	resp := &pb.RelationResp{}

	relationType := ""
	if req.Type != nil {
		relationType = *req.Type
	}
	sourceId := ""
	if req.SourceId != nil {
		sourceId = *req.SourceId
	}
	targetId := ""
	if req.TargetId != nil {
		targetId = *req.TargetId
	}
	var properties map[string]string
	if req.Properties != nil {
		properties = req.Properties
	}
	var description *string
	if req.Description != nil {
		description = req.Description
	}

	relation, err := s.KnowledgeService.UpdateRelation(ctx, req.Id, relationType, sourceId, targetId, properties, description)
	if err != nil {
		logger.Error("更新关系失败", logger.ErrorField(err))
		resp.BaseResp = buildErrorBaseResp(err.Error())
		return resp, nil
	}

	resp.BaseResp = buildSuccessBaseResp()
	resp.Relation = convertModelRelationToProtoRelation(relation)
	return resp, nil
}

// DeleteRelation implements the KnowledgeServiceImpl interface.
func (s *KnowledgeServiceImpl) DeleteRelation(ctx context.Context, req *pb.DeleteRelationReq) (*pbCommon.BaseResp, error) {
	err := s.KnowledgeService.DeleteRelation(ctx, req.Id)
	if err != nil {
		logger.Error("删除关系失败", logger.ErrorField(err))
		return buildErrorBaseResp(err.Error()), nil
	}
	return buildSuccessBaseResp(), nil
}

// GetRelation implements the KnowledgeServiceImpl interface.
func (s *KnowledgeServiceImpl) GetRelation(ctx context.Context, req *pb.GetByIdReq) (*pb.RelationResp, error) {
	resp := &pb.RelationResp{}

	relation, err := s.KnowledgeService.GetRelation(ctx, req.Id)
	if err != nil {
		logger.Error("获取关系失败", logger.ErrorField(err))
		resp.BaseResp = buildErrorBaseResp(err.Error())
		return resp, nil
	}

	resp.BaseResp = buildSuccessBaseResp()
	resp.Relation = convertModelRelationToProtoRelation(relation)
	return resp, nil
}

// QueryRelations implements the KnowledgeServiceImpl interface.
func (s *KnowledgeServiceImpl) QueryRelations(ctx context.Context, req *pb.QueryRelationReq) (*pb.BatchRelationResp, error) {
	resp := &pb.BatchRelationResp{}

	relationType := ""
	if req.Type != nil {
		relationType = *req.Type
	}
	sourceId := ""
	if req.SourceId != nil {
		sourceId = *req.SourceId
	}
	targetId := ""
	if req.TargetId != nil {
		targetId = *req.TargetId
	}

	relations, _, err := s.KnowledgeService.QueryRelations(ctx, relationType, sourceId, targetId, int(req.Page), int(req.PageSize))
	if err != nil {
		logger.Error("查询关系失败", logger.ErrorField(err))
		resp.BaseResp = buildErrorBaseResp(err.Error())
		return resp, nil
	}

	resp.BaseResp = buildSuccessBaseResp()
	resp.Relations = make([]*pb.Relation, 0, len(relations))
	for _, r := range relations {
		resp.Relations = append(resp.Relations, convertModelRelationToProtoRelation(r))
	}
	return resp, nil
}

// GetGraphStats implements the KnowledgeServiceImpl interface.
func (s *KnowledgeServiceImpl) GetGraphStats(ctx context.Context, req *pb.EmptyReq) (*pb.GraphStatsResp, error) {
	resp := &pb.GraphStatsResp{}

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
func (s *KnowledgeServiceImpl) GetRelatedEntities(ctx context.Context, req *pb.GetRelatedEntitiesReq) (*pb.BatchEntityResp, error) {
	resp := &pb.BatchEntityResp{}

	relationType := ""
	if req.RelationType != nil {
		relationType = *req.RelationType
	}

	entities, err := s.KnowledgeService.GetRelatedEntities(ctx, req.EntityId, relationType)
	if err != nil {
		logger.Error("获取相关实体失败", logger.ErrorField(err))
		resp.BaseResp = buildErrorBaseResp(err.Error())
		return resp, nil
	}

	resp.BaseResp = buildSuccessBaseResp()
	resp.Entities = make([]*pb.Entity, 0, len(entities))
	for _, e := range entities {
		resp.Entities = append(resp.Entities, convertModelEntityToProtoEntity(e))
	}
	return resp, nil
}

// ImportData implements the KnowledgeServiceImpl interface.
func (s *KnowledgeServiceImpl) ImportData(ctx context.Context, req *pb.ImportDataReq) (*pbCommon.BaseResp, error) {
	err := s.KnowledgeService.ImportData(ctx, req.DataType, req.Data)
	if err != nil {
		logger.Error("导入数据失败", logger.ErrorField(err))
		return buildErrorBaseResp(err.Error()), nil
	}
	return buildSuccessBaseResp(), nil
}

// SearchEntities implements the KnowledgeServiceImpl interface.
func (s *KnowledgeServiceImpl) SearchEntities(ctx context.Context, req *pb.SearchEntityReq) (*pb.BatchEntityResp, error) {
	resp := &pb.BatchEntityResp{}

	var entityType *string
	if req.Type != nil {
		entityType = req.Type
	}

	entities, _, err := s.KnowledgeService.SearchEntities(ctx, req.Keyword, entityType, int(req.Page), int(req.PageSize))
	if err != nil {
		logger.Error("搜索实体失败", logger.ErrorField(err))
		resp.BaseResp = buildErrorBaseResp(err.Error())
		return resp, nil
	}

	resp.BaseResp = buildSuccessBaseResp()
	resp.Entities = make([]*pb.Entity, 0, len(entities))
	for _, e := range entities {
		resp.Entities = append(resp.Entities, convertModelEntityToProtoEntity(e))
	}
	return resp, nil
}
