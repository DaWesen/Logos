package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"Logos/internal/service/ai/knowledge/dao"
	"Logos/internal/service/ai/knowledge/model"
	"Logos/pkg/auth"
	"Logos/pkg/cache"
	"Logos/pkg/es"
	"Logos/pkg/logger"
	"Logos/pkg/outbox"

	"gorm.io/gorm"
)

// getUserIDFromContext 从 gRPC context 中提取 user_id
func getUserIDFromContext(ctx context.Context) string {
	userID, err := auth.GetUserID(ctx)
	if err != nil {
		return ""
	}
	return userID
}

var (
	ErrEntityNotFound   = errors.New("实体不存在")
	ErrRelationNotFound = errors.New("关系不存在")
	ErrInternalServer   = errors.New("服务器内部错误")
)

type KnowledgeService interface {
	AddEntity(ctx context.Context, entityType, name, collectionID string, properties map[string]string, description *string, color string) (*model.Entity, error)
	FindOrCreateEntity(ctx context.Context, entityType, name, collectionID string, properties map[string]string, description *string, color string) (*model.Entity, error)
	UpdateEntity(ctx context.Context, id, entityType, name, collectionID string, properties map[string]string, description *string, color string) (*model.Entity, error)
	DeleteEntity(ctx context.Context, id string) error
	GetEntity(ctx context.Context, id string) (*model.Entity, error)
	QueryEntities(ctx context.Context, entityType, name, collectionID string, properties map[string]string, page, pageSize int) ([]*model.Entity, int64, error)
	SearchEntities(ctx context.Context, keyword string, entityType *string, collectionID string, page, pageSize int) ([]*model.Entity, int64, error)

	AddRelation(ctx context.Context, relationType, sourceId, targetId, collectionID string, properties map[string]string, description *string) (*model.Relation, error)
	UpdateRelation(ctx context.Context, id, relationType, sourceId, targetId string, properties map[string]string, description *string) (*model.Relation, error)
	DeleteRelation(ctx context.Context, id string) error
	GetRelation(ctx context.Context, id string) (*model.Relation, error)
	QueryRelations(ctx context.Context, relationType, sourceId, targetId, collectionID string, page, pageSize int) ([]*model.Relation, int64, error)

	GetGraphStats(ctx context.Context, collectionID string) (*model.GraphStats, error)
	GetRelatedEntities(ctx context.Context, entityId, relationType string) ([]*model.Entity, error)
	GetSubgraph(ctx context.Context, entityID string, depth int, collectionID string) (*model.Subgraph, error)
	GetEntityPaths(ctx context.Context, sourceID, targetID string, maxDepth int, collectionID string) ([]*model.EntityPath, error)

	ImportData(ctx context.Context, dataType string, data []string) error

	WithTransaction(ctx context.Context, fn func(txService KnowledgeService) error) error
}

type knowledgeServiceImpl struct {
	repo       dao.KnowledgeRepository
	cache      cache.Cache
	outboxRepo outbox.OutboxRepository
	esManager  *es.ESManager
}

func NewKnowledgeService(repo dao.KnowledgeRepository, cache cache.Cache, outboxRepo outbox.OutboxRepository, esManager *es.ESManager) KnowledgeService {
	return &knowledgeServiceImpl{
		repo:       repo,
		cache:      cache,
		outboxRepo: outboxRepo,
		esManager:  esManager,
	}
}

func (s *knowledgeServiceImpl) AddEntity(ctx context.Context, entityType, name, collectionID string, properties map[string]string, description *string, color string) (*model.Entity, error) {
	logger.Info("添加实体请求",
		logger.StringField("type", entityType),
		logger.StringField("name", name),
		logger.StringField("collectionId", collectionID))

	userID := getUserIDFromContext(ctx)
	entity := &model.Entity{
		Type:         entityType,
		Name:         name,
		CollectionID: collectionID,
		Color:        color,
		Properties:   model.JSONMap(properties),
		UserID:       userID,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	if description != nil {
		entity.Description = description
	}

	if err := s.repo.WithTransaction(ctx, func(txRepo dao.KnowledgeRepository) error {
		if err := txRepo.CreateEntity(ctx, entity); err != nil {
			return err
		}
		return s.saveEntityEventToOutbox(ctx, txRepo.DB(), "create", entity)
	}); err != nil {
		logger.Error("创建实体失败", logger.ErrorField(err))
		return nil, ErrInternalServer
	}

	s.cacheEntity(ctx, entity)
	s.indexEntityToES(ctx, entity)

	logger.Info("添加实体成功",
		logger.StringField("id", entity.ID),
		logger.StringField("type", entityType))

	return entity, nil
}

func (s *knowledgeServiceImpl) FindOrCreateEntity(ctx context.Context, entityType, name, collectionID string, properties map[string]string, description *string, color string) (*model.Entity, error) {
	existing, err := s.repo.FindEntityByNameAndType(ctx, name, entityType, collectionID, getUserIDFromContext(ctx))
	if err != nil {
		logger.Warn("查询已有实体失败",
			logger.StringField("name", name),
			logger.StringField("type", entityType),
			logger.ErrorField(err))
	}

	if existing != nil {
		needUpdate := false
		if description != nil && existing.Description == nil {
			existing.Description = description
			needUpdate = true
		}
		if properties != nil && len(properties) > 0 {
			if existing.Properties == nil {
				existing.Properties = model.JSONMap(properties)
				needUpdate = true
			} else {
				for k, v := range properties {
					if _, ok := existing.Properties[k]; !ok {
						existing.Properties[k] = v
						needUpdate = true
					}
				}
			}
		}

		if needUpdate {
			existing.UpdatedAt = time.Now()
			if err := s.repo.UpdateEntity(ctx, existing); err != nil {
				logger.Warn("更新已有实体失败",
					logger.StringField("id", existing.ID),
					logger.ErrorField(err))
			} else {
				s.cacheEntity(ctx, existing)
			}
		}

		logger.Info("复用已有实体",
			logger.StringField("id", existing.ID),
			logger.StringField("name", name),
			logger.StringField("type", entityType))

		return existing, nil
	}

	entity := &model.Entity{
		Type:         entityType,
		Name:         name,
		CollectionID: collectionID,
		Color:        color,
		Properties:   model.JSONMap(properties),
		UserID:       getUserIDFromContext(ctx),
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	if description != nil {
		entity.Description = description
	}

	if err := s.repo.WithTransaction(ctx, func(txRepo dao.KnowledgeRepository) error {
		if err := txRepo.CreateEntity(ctx, entity); err != nil {
			return err
		}
		return s.saveEntityEventToOutbox(ctx, txRepo.DB(), "create", entity)
	}); err != nil {
		logger.Error("创建实体失败", logger.ErrorField(err))
		return nil, ErrInternalServer
	}

	s.cacheEntity(ctx, entity)
	s.indexEntityToES(ctx, entity)

	logger.Info("创建新实体",
		logger.StringField("id", entity.ID),
		logger.StringField("name", name),
		logger.StringField("type", entityType),
		logger.StringField("collectionId", collectionID))

	return entity, nil
}

func (s *knowledgeServiceImpl) UpdateEntity(ctx context.Context, id, entityType, name, collectionID string, properties map[string]string, description *string, color string) (*model.Entity, error) {
	logger.Info("更新实体请求",
		logger.StringField("id", id))

	entity, err := s.repo.GetEntity(ctx, id)
	if err != nil {
		logger.Error("查询实体失败", logger.ErrorField(err))
		return nil, ErrInternalServer
	}
	if entity == nil {
		logger.Warn("实体不存在", logger.StringField("id", id))
		return nil, ErrEntityNotFound
	}

	if entityType != "" {
		entity.Type = entityType
	}
	if name != "" {
		entity.Name = name
	}
	if collectionID != "" {
		entity.CollectionID = collectionID
	}
	if properties != nil {
		entity.Properties = model.JSONMap(properties)
	}
	if description != nil {
		entity.Description = description
	}
	entity.UpdatedAt = time.Now()

	if err := s.repo.WithTransaction(ctx, func(txRepo dao.KnowledgeRepository) error {
		if err := txRepo.UpdateEntity(ctx, entity); err != nil {
			return err
		}
		return s.saveEntityEventToOutbox(ctx, txRepo.DB(), "update", entity)
	}); err != nil {
		logger.Error("更新实体失败", logger.ErrorField(err))
		return nil, ErrInternalServer
	}

	s.cacheEntity(ctx, entity)

	logger.Info("更新实体成功", logger.StringField("id", id))
	return entity, nil
}

func (s *knowledgeServiceImpl) DeleteEntity(ctx context.Context, id string) error {
	logger.Info("删除实体请求", logger.StringField("id", id))

	if err := s.repo.WithTransaction(ctx, func(txRepo dao.KnowledgeRepository) error {
		if err := txRepo.DeleteEntity(ctx, id); err != nil {
			return err
		}
		event := map[string]interface{}{
			"action":   "delete",
			"entityId": id,
		}
		return s.outboxRepo.SaveWithTx(ctx, txRepo.DB(), "knowledge_events", id, event)
	}); err != nil {
		logger.Error("删除实体失败", logger.ErrorField(err))
		return ErrInternalServer
	}

	cacheKey := fmt.Sprintf("entity:%s", id)
	if s.cache != nil {
		if err := s.cache.Delete(ctx, cacheKey); err != nil {
			logger.Warn("删除实体缓存失败",
				logger.StringField("id", id),
				logger.ErrorField(err))
		}
	}

	logger.Info("删除实体成功", logger.StringField("id", id))
	return nil
}

func (s *knowledgeServiceImpl) GetEntity(ctx context.Context, id string) (*model.Entity, error) {
	logger.Info("获取实体请求", logger.StringField("id", id))

	cacheKey := fmt.Sprintf("entity:%s", id)
	if s.cache != nil {
		if cached, err := s.cache.Get(ctx, cacheKey); err == nil && cached != "" {
			var entity model.Entity
			if err := json.Unmarshal([]byte(cached), &entity); err == nil {
				logger.Info("从缓存获取实体成功", logger.StringField("id", id))
				return &entity, nil
			}
		}
	}

	entity, err := s.repo.GetEntity(ctx, id)
	if err != nil {
		logger.Error("查询实体失败", logger.ErrorField(err))
		return nil, ErrInternalServer
	}
	if entity == nil {
		logger.Warn("实体不存在", logger.StringField("id", id))
		return nil, ErrEntityNotFound
	}

	s.cacheEntity(ctx, entity)
	return entity, nil
}

func (s *knowledgeServiceImpl) QueryEntities(ctx context.Context, entityType, name, collectionID string, properties map[string]string, page, pageSize int) ([]*model.Entity, int64, error) {
	logger.Info("查询实体请求",
		logger.StringField("type", entityType),
		logger.StringField("name", name),
		logger.StringField("collectionId", collectionID))

	userID := getUserIDFromContext(ctx)
	offset := (page - 1) * pageSize
	entities, err := s.repo.QueryEntities(ctx, entityType, name, collectionID, userID, properties, offset, pageSize)
	if err != nil {
		logger.Error("查询实体失败", logger.ErrorField(err))
		return nil, 0, ErrInternalServer
	}

	total, err := s.repo.CountEntities(ctx, entityType, name, collectionID, userID, properties)
	if err != nil {
		logger.Error("统计实体失败", logger.ErrorField(err))
		return nil, 0, ErrInternalServer
	}

	return entities, total, nil
}

func (s *knowledgeServiceImpl) AddRelation(ctx context.Context, relationType, sourceId, targetId, collectionID string, properties map[string]string, description *string) (*model.Relation, error) {
	logger.Info("添加关系请求",
		logger.StringField("type", relationType),
		logger.StringField("sourceId", sourceId),
		logger.StringField("targetId", targetId),
		logger.StringField("collectionId", collectionID))

	relation := &model.Relation{
		Type:         relationType,
		SourceID:     sourceId,
		TargetID:     targetId,
		CollectionID: collectionID,
		Properties:   model.JSONMap(properties),
		UserID:       getUserIDFromContext(ctx),
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	if description != nil {
		relation.Description = description
	}

	if err := s.repo.CreateRelation(ctx, relation); err != nil {
		logger.Error("创建关系失败", logger.ErrorField(err))
		return nil, ErrInternalServer
	}

	logger.Info("添加关系成功",
		logger.StringField("id", relation.ID))

	return relation, nil
}

func (s *knowledgeServiceImpl) UpdateRelation(ctx context.Context, id, relationType, sourceId, targetId string, properties map[string]string, description *string) (*model.Relation, error) {
	logger.Info("更新关系请求", logger.StringField("id", id))

	relation, err := s.repo.GetRelation(ctx, id)
	if err != nil {
		logger.Error("查询关系失败", logger.ErrorField(err))
		return nil, ErrInternalServer
	}
	if relation == nil {
		logger.Warn("关系不存在", logger.StringField("id", id))
		return nil, ErrRelationNotFound
	}

	if relationType != "" {
		relation.Type = relationType
	}
	if sourceId != "" {
		relation.SourceID = sourceId
	}
	if targetId != "" {
		relation.TargetID = targetId
	}
	if properties != nil {
		relation.Properties = model.JSONMap(properties)
	}
	if description != nil {
		relation.Description = description
	}
	relation.UpdatedAt = time.Now()

	if err := s.repo.UpdateRelation(ctx, relation); err != nil {
		logger.Error("更新关系失败", logger.ErrorField(err))
		return nil, ErrInternalServer
	}

	logger.Info("更新关系成功", logger.StringField("id", id))
	return relation, nil
}

func (s *knowledgeServiceImpl) DeleteRelation(ctx context.Context, id string) error {
	logger.Info("删除关系请求", logger.StringField("id", id))

	if err := s.repo.DeleteRelation(ctx, id); err != nil {
		logger.Error("删除关系失败", logger.ErrorField(err))
		return ErrInternalServer
	}

	logger.Info("删除关系成功", logger.StringField("id", id))
	return nil
}

func (s *knowledgeServiceImpl) GetRelation(ctx context.Context, id string) (*model.Relation, error) {
	logger.Info("获取关系请求", logger.StringField("id", id))

	relation, err := s.repo.GetRelation(ctx, id)
	if err != nil {
		logger.Error("查询关系失败", logger.ErrorField(err))
		return nil, ErrInternalServer
	}
	if relation == nil {
		logger.Warn("关系不存在", logger.StringField("id", id))
		return nil, ErrRelationNotFound
	}

	return relation, nil
}

func (s *knowledgeServiceImpl) QueryRelations(ctx context.Context, relationType, sourceId, targetId, collectionID string, page, pageSize int) ([]*model.Relation, int64, error) {
	logger.Info("查询关系请求",
		logger.StringField("type", relationType),
		logger.StringField("sourceId", sourceId),
		logger.StringField("targetId", targetId),
		logger.StringField("collectionId", collectionID))

	offset := (page - 1) * pageSize
	userID := getUserIDFromContext(ctx)
	relations, err := s.repo.QueryRelations(ctx, relationType, sourceId, targetId, collectionID, userID, offset, pageSize)
	if err != nil {
		logger.Error("查询关系失败", logger.ErrorField(err))
		return nil, 0, ErrInternalServer
	}

	total, err := s.repo.CountRelations(ctx, relationType, sourceId, targetId, collectionID, userID)
	if err != nil {
		logger.Error("统计关系失败", logger.ErrorField(err))
		return nil, 0, ErrInternalServer
	}

	return relations, total, nil
}

func (s *knowledgeServiceImpl) GetGraphStats(ctx context.Context, collectionID string) (*model.GraphStats, error) {
	logger.Info("获取图谱统计信息请求",
		logger.StringField("collectionId", collectionID))

	stats, err := s.repo.GetGraphStats(ctx, collectionID, getUserIDFromContext(ctx))
	if err != nil {
		logger.Error("获取图谱统计信息失败", logger.ErrorField(err))
		return nil, ErrInternalServer
	}

	return stats, nil
}

func (s *knowledgeServiceImpl) GetRelatedEntities(ctx context.Context, entityId, relationType string) ([]*model.Entity, error) {
	logger.Info("获取相关实体请求",
		logger.StringField("entityId", entityId),
		logger.StringField("relationType", relationType))

	entities, err := s.repo.GetRelatedEntities(ctx, entityId, relationType)
	if err != nil {
		logger.Error("获取相关实体失败", logger.ErrorField(err))
		return nil, ErrInternalServer
	}

	return entities, nil
}

func (s *knowledgeServiceImpl) GetSubgraph(ctx context.Context, entityID string, depth int, collectionID string) (*model.Subgraph, error) {
	logger.Info("获取子图请求",
		logger.StringField("entityId", entityID),
		logger.IntField("depth", depth),
		logger.StringField("collectionId", collectionID))

	subgraph, err := s.repo.GetSubgraph(ctx, entityID, depth, collectionID, getUserIDFromContext(ctx))
	if err != nil {
		logger.Error("获取子图失败", logger.ErrorField(err))
		return nil, ErrInternalServer
	}

	return subgraph, nil
}

func (s *knowledgeServiceImpl) GetEntityPaths(ctx context.Context, sourceID, targetID string, maxDepth int, collectionID string) ([]*model.EntityPath, error) {
	logger.Info("获取实体路径请求",
		logger.StringField("sourceId", sourceID),
		logger.StringField("targetId", targetID),
		logger.IntField("maxDepth", maxDepth))

	paths, err := s.repo.GetEntityPaths(ctx, sourceID, targetID, maxDepth, collectionID, getUserIDFromContext(ctx))
	if err != nil {
		logger.Error("获取实体路径失败", logger.ErrorField(err))
		return nil, ErrInternalServer
	}

	return paths, nil
}

func (s *knowledgeServiceImpl) WithTransaction(ctx context.Context, fn func(txService KnowledgeService) error) error {
	return s.repo.WithTransaction(ctx, func(txRepo dao.KnowledgeRepository) error {
		txService := NewKnowledgeService(txRepo, s.cache, s.outboxRepo, s.esManager)
		return fn(txService)
	})
}

func (s *knowledgeServiceImpl) ImportData(ctx context.Context, dataType string, data []string) error {
	logger.Info("导入数据请求",
		logger.StringField("dataType", dataType),
		logger.IntField("count", len(data)))

	if len(data) == 0 {
		return nil
	}

	switch dataType {
	case "entity":
		return s.importEntities(ctx, data)
	case "relation":
		return s.importRelations(ctx, data)
	default:
		logger.Warn("不支持的数据类型", logger.StringField("dataType", dataType))
		return fmt.Errorf("不支持的数据类型: %s", dataType)
	}
}

func (s *knowledgeServiceImpl) importEntities(ctx context.Context, data []string) error {
	for _, item := range data {
		var entityData map[string]interface{}
		if err := json.Unmarshal([]byte(item), &entityData); err != nil {
			logger.Warn("解析实体数据失败",
				logger.StringField("data", item),
				logger.ErrorField(err))
			continue
		}

		entityType, _ := entityData["type"].(string)
		name, _ := entityData["name"].(string)
		if entityType == "" || name == "" {
			logger.Warn("实体数据缺少必要字段", logger.StringField("data", item))
			continue
		}

		collectionID, _ := entityData["collection_id"].(string)

		properties := make(map[string]string)
		if props, ok := entityData["properties"].(map[string]interface{}); ok {
			for k, v := range props {
				if strV, ok := v.(string); ok {
					properties[k] = strV
				}
			}
		}

		var description *string
		if desc, ok := entityData["description"].(string); ok {
			description = &desc
		}

		if _, err := s.FindOrCreateEntity(ctx, entityType, name, collectionID, properties, description, ""); err != nil {
			logger.Warn("导入实体失败",
				logger.StringField("name", name),
				logger.ErrorField(err))
		}
	}

	logger.Info("导入实体完成", logger.IntField("count", len(data)))
	return nil
}

func (s *knowledgeServiceImpl) importRelations(ctx context.Context, data []string) error {
	for _, item := range data {
		var relationData map[string]interface{}
		if err := json.Unmarshal([]byte(item), &relationData); err != nil {
			logger.Warn("解析关系数据失败",
				logger.StringField("data", item),
				logger.ErrorField(err))
			continue
		}

		relationType, _ := relationData["type"].(string)
		sourceId, _ := relationData["sourceId"].(string)
		targetId, _ := relationData["targetId"].(string)
		if relationType == "" || sourceId == "" || targetId == "" {
			logger.Warn("关系数据缺少必要字段", logger.StringField("data", item))
			continue
		}

		properties := make(map[string]string)
		if props, ok := relationData["properties"].(map[string]interface{}); ok {
			for k, v := range props {
				if strV, ok := v.(string); ok {
					properties[k] = strV
				}
			}
		}

		var description *string
		if desc, ok := relationData["description"].(string); ok {
			description = &desc
		}

		if _, err := s.AddRelation(ctx, relationType, sourceId, targetId, "", properties, description); err != nil {
			logger.Warn("导入关系失败",
				logger.StringField("type", relationType),
				logger.ErrorField(err))
		}
	}

	logger.Info("导入关系完成", logger.IntField("count", len(data)))
	return nil
}

func (s *knowledgeServiceImpl) SearchEntities(ctx context.Context, keyword string, entityType *string, collectionID string, page, pageSize int) ([]*model.Entity, int64, error) {
	logger.Info("搜索实体请求",
		logger.StringField("keyword", keyword),
		logger.StringField("collectionId", collectionID),
		logger.IntField("page", page),
		logger.IntField("pageSize", pageSize))

	if s.esManager == nil {
		logger.Warn("ES未初始化，降级到数据库搜索")
		return s.searchEntitiesFallback(ctx, keyword, entityType, collectionID, page, pageSize)
	}

	from := (page - 1) * pageSize

	query := map[string]interface{}{
		"bool": map[string]interface{}{
			"must": []map[string]interface{}{
				{
					"multi_match": map[string]interface{}{
						"query":  keyword,
						"fields": []string{"name", "description"},
						"type":   "best_fields",
					},
				},
			},
		},
	}

	filters := []map[string]interface{}{}
	if entityType != nil && *entityType != "" {
		filters = append(filters, map[string]interface{}{
			"term": map[string]interface{}{
				"type": *entityType,
			},
		})
	}
	if collectionID != "" {
		filters = append(filters, map[string]interface{}{
			"term": map[string]interface{}{
				"collectionId": collectionID,
			},
		})
	}
	if len(filters) > 0 {
		boolQuery := query["bool"].(map[string]interface{})
		boolQuery["filter"] = filters
	}

	searchQuery := es.SearchQuery{
		Query: query,
		From:  from,
		Size:  pageSize,
	}

	var result es.SearchResult
	if err := s.esManager.Search("entities", searchQuery, &result); err != nil {
		logger.Warn("ES搜索失败，降级到数据库搜索", logger.ErrorField(err))
		return s.searchEntitiesFallback(ctx, keyword, entityType, collectionID, page, pageSize)
	}

	var entities []*model.Entity
	for _, hit := range result.Hits.Hits {
		var entity model.Entity
		if err := json.Unmarshal(hit.Source, &entity); err == nil {
			entities = append(entities, &entity)
		}
	}

	if len(entities) == 0 {
		logger.Info("ES搜索无结果，降级到数据库搜索")
		return s.searchEntitiesFallback(ctx, keyword, entityType, collectionID, page, pageSize)
	}

	logger.Info("ES搜索实体成功",
		logger.IntField("count", len(entities)),
		logger.Int64Field("total", result.Hits.Total.Value))

	return entities, result.Hits.Total.Value, nil
}

func (s *knowledgeServiceImpl) searchEntitiesFallback(ctx context.Context, keyword string, entityType *string, collectionID string, page, pageSize int) ([]*model.Entity, int64, error) {
	et := ""
	if entityType != nil {
		et = *entityType
	}
	entities, total, err := s.QueryEntities(ctx, et, keyword, collectionID, nil, page, pageSize)
	if err != nil {
		return nil, 0, err
	}
	return entities, total, nil
}

func (s *knowledgeServiceImpl) cacheEntity(ctx context.Context, entity *model.Entity) {
	if s.cache == nil {
		return
	}
	cacheKey := fmt.Sprintf("entity:%s", entity.ID)
	entityJSON, _ := json.Marshal(entity)
	if err := s.cache.Set(ctx, cacheKey, string(entityJSON), 1*time.Hour); err != nil {
		logger.Warn("缓存实体失败",
			logger.StringField("id", entity.ID),
			logger.ErrorField(err))
	}
}

func (s *knowledgeServiceImpl) indexEntityToES(ctx context.Context, entity *model.Entity) {
	if s.esManager == nil {
		return
	}
	if err := s.esManager.AddDocument("entities", entity.ID, entity); err != nil {
		logger.Warn("同步实体到ES失败",
			logger.StringField("id", entity.ID),
			logger.ErrorField(err))
	}
}

func (s *knowledgeServiceImpl) saveEntityEventToOutbox(ctx context.Context, db *gorm.DB, action string, entity *model.Entity) error {
	event := map[string]interface{}{
		"action": action,
		"entity": entity,
	}
	return s.outboxRepo.SaveWithTx(ctx, db, "knowledge_events", entity.ID, event)
}
