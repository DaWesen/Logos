package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"Logos/internal/ai/knowledge/dao"
	"Logos/internal/ai/knowledge/model"
	"Logos/pkg/cache"
	"Logos/pkg/es"
	"Logos/pkg/logger"
	"Logos/pkg/mq"
)

var (
	ErrEntityNotFound   = errors.New("实体不存在")
	ErrRelationNotFound = errors.New("关系不存在")
	ErrInternalServer   = errors.New("服务器内部错误")
)

type KnowledgeService interface {
	AddEntity(ctx context.Context, entityType, name string, properties map[string]string, description *string) (*model.Entity, error)
	UpdateEntity(ctx context.Context, id, entityType, name string, properties map[string]string, description *string) (*model.Entity, error)
	DeleteEntity(ctx context.Context, id string) error
	GetEntity(ctx context.Context, id string) (*model.Entity, error)
	QueryEntities(ctx context.Context, entityType, name string, properties map[string]string, page, pageSize int) ([]*model.Entity, int64, error)
	SearchEntities(ctx context.Context, keyword string, entityType *string, page, pageSize int) ([]*model.Entity, int64, error)

	AddRelation(ctx context.Context, relationType, sourceId, targetId string, properties map[string]string, description *string) (*model.Relation, error)
	UpdateRelation(ctx context.Context, id, relationType, sourceId, targetId string, properties map[string]string, description *string) (*model.Relation, error)
	DeleteRelation(ctx context.Context, id string) error
	GetRelation(ctx context.Context, id string) (*model.Relation, error)
	QueryRelations(ctx context.Context, relationType, sourceId, targetId string, page, pageSize int) ([]*model.Relation, int64, error)

	GetGraphStats(ctx context.Context) (*model.GraphStats, error)
	GetRelatedEntities(ctx context.Context, entityId, relationType string) ([]*model.Entity, error)

	ImportData(ctx context.Context, dataType string, data []string) error

	WithTransaction(ctx context.Context, fn func(txService KnowledgeService) error) error
}

type knowledgeServiceImpl struct {
	repo      dao.KnowledgeRepository
	cache     cache.Cache
	producer  *mq.Producer
	esManager *es.ESManager
}

func NewKnowledgeService(repo dao.KnowledgeRepository, cache cache.Cache, producer *mq.Producer, esManager *es.ESManager) KnowledgeService {
	return &knowledgeServiceImpl{
		repo:      repo,
		cache:     cache,
		producer:  producer,
		esManager: esManager,
	}
}

func (s *knowledgeServiceImpl) AddEntity(ctx context.Context, entityType, name string, properties map[string]string, description *string) (*model.Entity, error) {
	logger.Info("添加实体请求",
		logger.StringField("type", entityType),
		logger.StringField("name", name))

	entity := &model.Entity{
		Type:       entityType,
		Name:       name,
		Properties: model.JSONMap(properties),
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	if description != nil {
		entity.Description = description
	}

	if err := s.repo.CreateEntity(ctx, entity); err != nil {
		logger.Error("创建实体失败", logger.ErrorField(err))
		return nil, ErrInternalServer
	}

	cacheKey := fmt.Sprintf("entity:%s", entity.ID)
	entityJSON, _ := json.Marshal(entity)
	if s.cache != nil {
		if err := s.cache.Set(ctx, cacheKey, string(entityJSON), 1*time.Hour); err != nil {
			logger.Warn("缓存实体失败",
				logger.StringField("id", entity.ID),
				logger.ErrorField(err))
		}
	}

	if s.producer != nil {
		event := map[string]interface{}{
			"action": "create",
			"entity": entity,
		}
		eventJSON, _ := json.Marshal(event)
		if err := s.producer.SendKnowledgeEvent(ctx, entity.ID, eventJSON); err != nil {
			logger.Warn("发送知识变更事件失败",
				logger.StringField("id", entity.ID),
				logger.ErrorField(err))
		}
	}

	logger.Info("添加实体成功",
		logger.StringField("id", entity.ID),
		logger.StringField("type", entityType))

	return entity, nil
}

func (s *knowledgeServiceImpl) UpdateEntity(ctx context.Context, id, entityType, name string, properties map[string]string, description *string) (*model.Entity, error) {
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
	if properties != nil {
		entity.Properties = model.JSONMap(properties)
	}
	if description != nil {
		entity.Description = description
	}
	entity.UpdatedAt = time.Now()

	if err := s.repo.UpdateEntity(ctx, entity); err != nil {
		logger.Error("更新实体失败", logger.ErrorField(err))
		return nil, ErrInternalServer
	}

	cacheKey := fmt.Sprintf("entity:%s", id)
	entityJSON, _ := json.Marshal(entity)
	if s.cache != nil {
		if err := s.cache.Set(ctx, cacheKey, string(entityJSON), 1*time.Hour); err != nil {
			logger.Warn("更新实体缓存失败",
				logger.StringField("id", id),
				logger.ErrorField(err))
		}
	}

	if s.producer != nil {
		event := map[string]interface{}{
			"action": "update",
			"entity": entity,
		}
		eventJSON, _ := json.Marshal(event)
		if err := s.producer.SendKnowledgeEvent(ctx, id, eventJSON); err != nil {
			logger.Warn("发送知识变更事件失败",
				logger.StringField("id", id),
				logger.ErrorField(err))
		}
	}

	logger.Info("更新实体成功", logger.StringField("id", id))
	return entity, nil
}

func (s *knowledgeServiceImpl) DeleteEntity(ctx context.Context, id string) error {
	logger.Info("删除实体请求", logger.StringField("id", id))

	if err := s.repo.DeleteEntity(ctx, id); err != nil {
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

	if s.producer != nil {
		event := map[string]interface{}{
			"action":   "delete",
			"entityId": id,
		}
		eventJSON, _ := json.Marshal(event)
		if err := s.producer.SendKnowledgeEvent(ctx, id, eventJSON); err != nil {
			logger.Warn("发送知识变更事件失败",
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

	if s.cache != nil {
		entityJSON, _ := json.Marshal(entity)
		if err := s.cache.Set(ctx, cacheKey, string(entityJSON), 1*time.Hour); err != nil {
			logger.Warn("缓存实体失败",
				logger.StringField("id", id),
				logger.ErrorField(err))
		}
	}

	return entity, nil
}

func (s *knowledgeServiceImpl) QueryEntities(ctx context.Context, entityType, name string, properties map[string]string, page, pageSize int) ([]*model.Entity, int64, error) {
	logger.Info("查询实体请求",
		logger.StringField("type", entityType),
		logger.StringField("name", name))

	offset := (page - 1) * pageSize
	entities, err := s.repo.QueryEntities(ctx, entityType, name, properties, offset, pageSize)
	if err != nil {
		logger.Error("查询实体失败", logger.ErrorField(err))
		return nil, 0, ErrInternalServer
	}

	total, err := s.repo.CountEntities(ctx, entityType, name, properties)
	if err != nil {
		logger.Error("统计实体失败", logger.ErrorField(err))
		return nil, 0, ErrInternalServer
	}

	return entities, total, nil
}

func (s *knowledgeServiceImpl) AddRelation(ctx context.Context, relationType, sourceId, targetId string, properties map[string]string, description *string) (*model.Relation, error) {
	logger.Info("添加关系请求",
		logger.StringField("type", relationType),
		logger.StringField("sourceId", sourceId),
		logger.StringField("targetId", targetId))

	relation := &model.Relation{
		Type:       relationType,
		SourceID:   sourceId,
		TargetID:   targetId,
		Properties: model.JSONMap(properties),
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
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

func (s *knowledgeServiceImpl) QueryRelations(ctx context.Context, relationType, sourceId, targetId string, page, pageSize int) ([]*model.Relation, int64, error) {
	logger.Info("查询关系请求",
		logger.StringField("type", relationType),
		logger.StringField("sourceId", sourceId),
		logger.StringField("targetId", targetId))

	offset := (page - 1) * pageSize
	relations, err := s.repo.QueryRelations(ctx, relationType, sourceId, targetId, offset, pageSize)
	if err != nil {
		logger.Error("查询关系失败", logger.ErrorField(err))
		return nil, 0, ErrInternalServer
	}

	total, err := s.repo.CountRelations(ctx, relationType, sourceId, targetId)
	if err != nil {
		logger.Error("统计关系失败", logger.ErrorField(err))
		return nil, 0, ErrInternalServer
	}

	return relations, total, nil
}

func (s *knowledgeServiceImpl) GetGraphStats(ctx context.Context) (*model.GraphStats, error) {
	logger.Info("获取图谱统计信息请求")

	stats, err := s.repo.GetGraphStats(ctx)
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

func (s *knowledgeServiceImpl) WithTransaction(ctx context.Context, fn func(txService KnowledgeService) error) error {
	return s.repo.WithTransaction(ctx, func(txRepo dao.KnowledgeRepository) error {
		txService := NewKnowledgeService(txRepo, s.cache, s.producer, s.esManager)
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

		if _, err := s.AddEntity(ctx, entityType, name, properties, description); err != nil {
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

		if _, err := s.AddRelation(ctx, relationType, sourceId, targetId, properties, description); err != nil {
			logger.Warn("导入关系失败",
				logger.StringField("type", relationType),
				logger.ErrorField(err))
		}
	}

	logger.Info("导入关系完成", logger.IntField("count", len(data)))
	return nil
}

func (s *knowledgeServiceImpl) SearchEntities(ctx context.Context, keyword string, entityType *string, page, pageSize int) ([]*model.Entity, int64, error) {
	logger.Info("搜索实体请求",
		logger.StringField("keyword", keyword),
		logger.IntField("page", page),
		logger.IntField("pageSize", pageSize))

	if s.esManager == nil {
		logger.Warn("ES未初始化，降级到数据库搜索")
		return s.searchEntitiesFallback(ctx, keyword, entityType, page, pageSize)
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

	if entityType != nil && *entityType != "" {
		boolQuery := query["bool"].(map[string]interface{})
		boolQuery["filter"] = []map[string]interface{}{
			{
				"term": map[string]interface{}{
					"type": *entityType,
				},
			},
		}
	}

	searchQuery := es.SearchQuery{
		Query: query,
		From:  from,
		Size:  pageSize,
	}

	var result es.SearchResult
	if err := s.esManager.Search("entities", searchQuery, &result); err != nil {
		logger.Warn("ES搜索失败，降级到数据库搜索", logger.ErrorField(err))
		return s.searchEntitiesFallback(ctx, keyword, entityType, page, pageSize)
	}

	var entities []*model.Entity
	for _, hit := range result.Hits.Hits {
		var entity model.Entity
		if err := json.Unmarshal(hit.Source, &entity); err == nil {
			entities = append(entities, &entity)
		}
	}

	logger.Info("ES搜索实体成功",
		logger.IntField("count", len(entities)),
		logger.Int64Field("total", result.Hits.Total.Value))

	return entities, result.Hits.Total.Value, nil
}

func (s *knowledgeServiceImpl) searchEntitiesFallback(ctx context.Context, keyword string, entityType *string, page, pageSize int) ([]*model.Entity, int64, error) {
	et := ""
	if entityType != nil {
		et = *entityType
	}
	entities, total, err := s.QueryEntities(ctx, et, keyword, nil, page, pageSize)
	if err != nil {
		return nil, 0, err
	}
	return entities, total, nil
}
