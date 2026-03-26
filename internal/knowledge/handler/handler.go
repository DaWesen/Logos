package handler

import (
	common "Noah/kitex_gen/common"
	knowledge "Noah/kitex_gen/knowledge"
	"context"
)

// KnowledgeServiceImpl implements the last service interface defined in the IDL.
type KnowledgeServiceImpl struct{}

// AddEntity implements the KnowledgeServiceImpl interface.
func (s *KnowledgeServiceImpl) AddEntity(ctx context.Context, req *knowledge.AddEntityReq) (resp *knowledge.EntityResp, err error) {
	// TODO: Your code here...
	return
}

// UpdateEntity implements the KnowledgeServiceImpl interface.
func (s *KnowledgeServiceImpl) UpdateEntity(ctx context.Context, req *knowledge.UpdateEntityReq) (resp *knowledge.EntityResp, err error) {
	// TODO: Your code here...
	return
}

// DeleteEntity implements the KnowledgeServiceImpl interface.
func (s *KnowledgeServiceImpl) DeleteEntity(ctx context.Context, req *knowledge.DeleteEntityReq) (resp *common.BaseResp, err error) {
	// TODO: Your code here...
	return
}

// GetEntity implements the KnowledgeServiceImpl interface.
func (s *KnowledgeServiceImpl) GetEntity(ctx context.Context, id string) (resp *knowledge.EntityResp, err error) {
	// TODO: Your code here...
	return
}

// QueryEntities implements the KnowledgeServiceImpl interface.
func (s *KnowledgeServiceImpl) QueryEntities(ctx context.Context, req *knowledge.QueryEntityReq) (resp *knowledge.BatchEntityResp, err error) {
	// TODO: Your code here...
	return
}

// AddRelation implements the KnowledgeServiceImpl interface.
func (s *KnowledgeServiceImpl) AddRelation(ctx context.Context, req *knowledge.AddRelationReq) (resp *knowledge.RelationResp, err error) {
	// TODO: Your code here...
	return
}

// UpdateRelation implements the KnowledgeServiceImpl interface.
func (s *KnowledgeServiceImpl) UpdateRelation(ctx context.Context, req *knowledge.UpdateRelationReq) (resp *knowledge.RelationResp, err error) {
	// TODO: Your code here...
	return
}

// DeleteRelation implements the KnowledgeServiceImpl interface.
func (s *KnowledgeServiceImpl) DeleteRelation(ctx context.Context, req *knowledge.DeleteRelationReq) (resp *common.BaseResp, err error) {
	// TODO: Your code here...
	return
}

// GetRelation implements the KnowledgeServiceImpl interface.
func (s *KnowledgeServiceImpl) GetRelation(ctx context.Context, id string) (resp *knowledge.RelationResp, err error) {
	// TODO: Your code here...
	return
}

// QueryRelations implements the KnowledgeServiceImpl interface.
func (s *KnowledgeServiceImpl) QueryRelations(ctx context.Context, req *knowledge.QueryRelationReq) (resp *knowledge.BatchRelationResp, err error) {
	// TODO: Your code here...
	return
}

// GetGraphStats implements the KnowledgeServiceImpl interface.
func (s *KnowledgeServiceImpl) GetGraphStats(ctx context.Context) (resp *knowledge.GraphStatsResp, err error) {
	// TODO: Your code here...
	return
}

// GetRelatedEntities implements the KnowledgeServiceImpl interface.
func (s *KnowledgeServiceImpl) GetRelatedEntities(ctx context.Context, entityId string, relationType string) (resp *knowledge.BatchEntityResp, err error) {
	// TODO: Your code here...
	return
}

// ImportData implements the KnowledgeServiceImpl interface.
func (s *KnowledgeServiceImpl) ImportData(ctx context.Context, req *knowledge.ImportDataReq) (resp *common.BaseResp, err error) {
	// TODO: Your code here...
	return
}
