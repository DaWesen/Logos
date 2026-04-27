package dao

import (
	"context"
	"errors"

	"Logos/internal/ai/knowledge/model"
	"Logos/pkg/graph"
	"Logos/pkg/logger"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

var (
	ErrEntityNotFound   = errors.New("实体不存在")
	ErrRelationNotFound = errors.New("关系不存在")
)

type KnowledgeRepository interface {
	CreateEntity(ctx context.Context, entity *model.Entity) error
	UpdateEntity(ctx context.Context, entity *model.Entity) error
	DeleteEntity(ctx context.Context, id string) error
	GetEntity(ctx context.Context, id string) (*model.Entity, error)
	QueryEntities(ctx context.Context, entityType, name string, properties map[string]string, offset, limit int) ([]*model.Entity, error)
	CountEntities(ctx context.Context, entityType, name string, properties map[string]string) (int64, error)

	CreateRelation(ctx context.Context, relation *model.Relation) error
	UpdateRelation(ctx context.Context, relation *model.Relation) error
	DeleteRelation(ctx context.Context, id string) error
	GetRelation(ctx context.Context, id string) (*model.Relation, error)
	QueryRelations(ctx context.Context, relationType, sourceId, targetId string, offset, limit int) ([]*model.Relation, error)
	CountRelations(ctx context.Context, relationType, sourceId, targetId string) (int64, error)

	GetGraphStats(ctx context.Context) (*model.GraphStats, error)
	GetRelatedEntities(ctx context.Context, entityId, relationType string) ([]*model.Entity, error)

	WithTransaction(ctx context.Context, fn func(txRepo KnowledgeRepository) error) error
}

type knowledgeRepository struct {
	db           *gorm.DB
	neo4jManager *graph.Neo4jManager
}

func NewKnowledgeRepository(db *gorm.DB, neo4jManager *graph.Neo4jManager) KnowledgeRepository {
	return &knowledgeRepository{db: db, neo4jManager: neo4jManager}
}

func (r *knowledgeRepository) CreateEntity(ctx context.Context, entity *model.Entity) error {
	if entity.ID == "" {
		entity.ID = uuid.NewString()
	}

	if err := r.db.WithContext(ctx).Create(entity).Error; err != nil {
		return err
	}

	if r.neo4jManager != nil {
		properties := map[string]interface{}{
			"name":        entity.Name,
			"description": entity.Description,
			"createdAt":   entity.CreatedAt.Unix(),
			"updatedAt":   entity.UpdatedAt.Unix(),
		}
		for k, v := range entity.Properties {
			properties[k] = v
		}

		if err := r.neo4jManager.CreateNode(ctx, entity.Type, entity.ID, properties); err != nil {
			logger.Warn("同步实体到Neo4j失败",
				logger.StringField("id", entity.ID),
				logger.ErrorField(err))
		}
	}

	return nil
}

func (r *knowledgeRepository) UpdateEntity(ctx context.Context, entity *model.Entity) error {
	if err := r.db.WithContext(ctx).Save(entity).Error; err != nil {
		return err
	}

	if r.neo4jManager != nil {
		properties := map[string]interface{}{
			"name":        entity.Name,
			"description": entity.Description,
			"updatedAt":   entity.UpdatedAt.Unix(),
		}
		for k, v := range entity.Properties {
			properties[k] = v
		}

		if err := r.neo4jManager.UpdateNode(ctx, entity.Type, entity.ID, properties); err != nil {
			logger.Warn("同步实体到Neo4j失败",
				logger.StringField("id", entity.ID),
				logger.ErrorField(err))
		}
	}

	return nil
}

func (r *knowledgeRepository) DeleteEntity(ctx context.Context, id string) error {
	entity, err := r.GetEntity(ctx, id)
	if err != nil {
		return err
	}

	if err := r.db.WithContext(ctx).Delete(&model.Entity{}, "id = ?", id).Error; err != nil {
		return err
	}

	if r.neo4jManager != nil && entity != nil {
		if err := r.neo4jManager.DeleteNode(ctx, entity.Type, id); err != nil {
			logger.Warn("从Neo4j删除实体失败",
				logger.StringField("id", id),
				logger.ErrorField(err))
		}
	}

	return nil
}

func (r *knowledgeRepository) GetEntity(ctx context.Context, id string) (*model.Entity, error) {
	var entity model.Entity
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&entity).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &entity, nil
}

func (r *knowledgeRepository) QueryEntities(ctx context.Context, entityType, name string, properties map[string]string, offset, limit int) ([]*model.Entity, error) {
	query := r.db.WithContext(ctx).Model(&model.Entity{})

	if entityType != "" {
		query = query.Where("type = ?", entityType)
	}
	if name != "" {
		query = query.Where("name LIKE ?", "%"+name+"%")
	}

	var entities []*model.Entity
	err := query.Offset(offset).Limit(limit).Order("created_at DESC").Find(&entities).Error
	return entities, err
}

func (r *knowledgeRepository) CountEntities(ctx context.Context, entityType, name string, properties map[string]string) (int64, error) {
	query := r.db.WithContext(ctx).Model(&model.Entity{})

	if entityType != "" {
		query = query.Where("type = ?", entityType)
	}
	if name != "" {
		query = query.Where("name LIKE ?", "%"+name+"%")
	}

	var count int64
	err := query.Count(&count).Error
	return count, err
}

func (r *knowledgeRepository) CreateRelation(ctx context.Context, relation *model.Relation) error {
	if relation.ID == "" {
		relation.ID = uuid.NewString()
	}

	if err := r.db.WithContext(ctx).Create(relation).Error; err != nil {
		return err
	}

	if r.neo4jManager != nil {
		sourceEntity, _ := r.GetEntity(ctx, relation.SourceID)
		targetEntity, _ := r.GetEntity(ctx, relation.TargetID)

		if sourceEntity != nil && targetEntity != nil {
			properties := map[string]interface{}{
				"description": relation.Description,
				"createdAt":   relation.CreatedAt.Unix(),
				"updatedAt":   relation.UpdatedAt.Unix(),
			}
			for k, v := range relation.Properties {
				properties[k] = v
			}

			if err := r.neo4jManager.CreateRelationship(ctx, sourceEntity.Type, relation.SourceID, relation.Type, targetEntity.Type, relation.TargetID, properties); err != nil {
				logger.Warn("同步关系到Neo4j失败",
					logger.StringField("id", relation.ID),
					logger.ErrorField(err))
			}
		}
	}

	return nil
}

func (r *knowledgeRepository) UpdateRelation(ctx context.Context, relation *model.Relation) error {
	oldRelation, err := r.GetRelation(ctx, relation.ID)
	if err != nil {
		return err
	}

	if err := r.db.WithContext(ctx).Save(relation).Error; err != nil {
		return err
	}

	if r.neo4jManager != nil && oldRelation != nil {
		sourceEntity, _ := r.GetEntity(ctx, relation.SourceID)
		targetEntity, _ := r.GetEntity(ctx, relation.TargetID)
		oldSourceEntity, _ := r.GetEntity(ctx, oldRelation.SourceID)
		oldTargetEntity, _ := r.GetEntity(ctx, oldRelation.TargetID)

		if oldSourceEntity != nil && oldTargetEntity != nil {
			if err := r.neo4jManager.DeleteRelationship(ctx, oldSourceEntity.Type, oldRelation.SourceID, oldRelation.Type, oldTargetEntity.Type, oldRelation.TargetID); err != nil {
				logger.Warn("从Neo4j删除旧关系失败",
					logger.StringField("id", relation.ID),
					logger.ErrorField(err))
			}
		}

		if sourceEntity != nil && targetEntity != nil {
			properties := map[string]interface{}{
				"description": relation.Description,
				"updatedAt":   relation.UpdatedAt.Unix(),
			}
			for k, v := range relation.Properties {
				properties[k] = v
			}

			if err := r.neo4jManager.CreateRelationship(ctx, sourceEntity.Type, relation.SourceID, relation.Type, targetEntity.Type, relation.TargetID, properties); err != nil {
				logger.Warn("同步更新后的关系到Neo4j失败",
					logger.StringField("id", relation.ID),
					logger.ErrorField(err))
			}
		}
	}

	return nil
}

func (r *knowledgeRepository) DeleteRelation(ctx context.Context, id string) error {
	relation, err := r.GetRelation(ctx, id)
	if err != nil {
		return err
	}

	if err := r.db.WithContext(ctx).Delete(&model.Relation{}, "id = ?", id).Error; err != nil {
		return err
	}

	if r.neo4jManager != nil && relation != nil {
		sourceEntity, _ := r.GetEntity(ctx, relation.SourceID)
		targetEntity, _ := r.GetEntity(ctx, relation.TargetID)

		if sourceEntity != nil && targetEntity != nil {
			if err := r.neo4jManager.DeleteRelationship(ctx, sourceEntity.Type, relation.SourceID, relation.Type, targetEntity.Type, relation.TargetID); err != nil {
				logger.Warn("从Neo4j删除关系失败",
					logger.StringField("id", id),
					logger.ErrorField(err))
			}
		}
	}

	return nil
}

func (r *knowledgeRepository) GetRelation(ctx context.Context, id string) (*model.Relation, error) {
	var relation model.Relation
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&relation).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &relation, nil
}

func (r *knowledgeRepository) QueryRelations(ctx context.Context, relationType, sourceId, targetId string, offset, limit int) ([]*model.Relation, error) {
	query := r.db.WithContext(ctx).Model(&model.Relation{})

	if relationType != "" {
		query = query.Where("type = ?", relationType)
	}
	if sourceId != "" {
		query = query.Where("source_id = ?", sourceId)
	}
	if targetId != "" {
		query = query.Where("target_id = ?", targetId)
	}

	var relations []*model.Relation
	err := query.Offset(offset).Limit(limit).Order("created_at DESC").Find(&relations).Error
	return relations, err
}

func (r *knowledgeRepository) CountRelations(ctx context.Context, relationType, sourceId, targetId string) (int64, error) {
	query := r.db.WithContext(ctx).Model(&model.Relation{})

	if relationType != "" {
		query = query.Where("type = ?", relationType)
	}
	if sourceId != "" {
		query = query.Where("source_id = ?", sourceId)
	}
	if targetId != "" {
		query = query.Where("target_id = ?", targetId)
	}

	var count int64
	err := query.Count(&count).Error
	return count, err
}

func (r *knowledgeRepository) GetGraphStats(ctx context.Context) (*model.GraphStats, error) {
	stats := &model.GraphStats{
		EntityTypeCount:   make(map[string]int64),
		RelationTypeCount: make(map[string]int64),
	}

	if err := r.db.WithContext(ctx).Model(&model.Entity{}).Count(&stats.EntityCount).Error; err != nil {
		return nil, err
	}

	if err := r.db.WithContext(ctx).Model(&model.Relation{}).Count(&stats.RelationCount).Error; err != nil {
		return nil, err
	}

	type EntityTypeCount struct {
		Type  string
		Count int64
	}
	var entityTypeCounts []EntityTypeCount
	if err := r.db.WithContext(ctx).Model(&model.Entity{}).Select("type, count(*) as count").Group("type").Find(&entityTypeCounts).Error; err != nil {
		logger.Error("获取实体类型统计失败", logger.ErrorField(err))
	} else {
		for etc := range entityTypeCounts {
			stats.EntityTypeCount[entityTypeCounts[etc].Type] = entityTypeCounts[etc].Count
		}
	}

	type RelationTypeCount struct {
		Type  string
		Count int64
	}
	var relationTypeCounts []RelationTypeCount
	if err := r.db.WithContext(ctx).Model(&model.Relation{}).Select("type, count(*) as count").Group("type").Find(&relationTypeCounts).Error; err != nil {
		logger.Error("获取关系类型统计失败", logger.ErrorField(err))
	} else {
		for rtc := range relationTypeCounts {
			stats.RelationTypeCount[relationTypeCounts[rtc].Type] = relationTypeCounts[rtc].Count
		}
	}

	return stats, nil
}

func (r *knowledgeRepository) GetRelatedEntities(ctx context.Context, entityId, relationType string) ([]*model.Entity, error) {
	if r.neo4jManager != nil {
		entity, err := r.GetEntity(ctx, entityId)
		if err == nil && entity != nil {
			nodes, err := r.neo4jManager.GetRelatedNodes(ctx, entity.Type, entityId, relationType)
			if err == nil {
				var entities []*model.Entity
				for _, node := range nodes {
					id, _ := node["id"].(string)
					if id != "" {
						e, _ := r.GetEntity(ctx, id)
						if e != nil {
							entities = append(entities, e)
						}
					}
				}
				if len(entities) > 0 {
					return entities, nil
				}
			}
		}
	}

	query := `
		SELECT e.* FROM entities e
		INNER JOIN relations r ON e.id = r.target_id
		WHERE r.source_id = ?
	`
	args := []interface{}{entityId}

	if relationType != "" {
		query += " AND r.type = ?"
		args = append(args, relationType)
	}

	var entities []*model.Entity
	err := r.db.WithContext(ctx).Raw(query, args...).Find(&entities).Error
	if err != nil {
		return nil, err
	}

	query = `
		SELECT e.* FROM entities e
		INNER JOIN relations r ON e.id = r.source_id
		WHERE r.target_id = ?
	`
	if relationType != "" {
		query += " AND r.type = ?"
	}

	var reverseEntities []*model.Entity
	err = r.db.WithContext(ctx).Raw(query, args...).Find(&reverseEntities).Error
	if err != nil {
		return nil, err
	}

	entities = append(entities, reverseEntities...)
	return entities, nil
}

func (r *knowledgeRepository) WithTransaction(ctx context.Context, fn func(txRepo KnowledgeRepository) error) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		txRepo := NewKnowledgeRepository(tx, r.neo4jManager)
		return fn(txRepo)
	})
}
