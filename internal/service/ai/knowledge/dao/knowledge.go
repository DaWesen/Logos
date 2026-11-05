package dao

import (
	"context"
	"errors"
	"fmt"

	"Logos/internal/service/ai/knowledge/model"
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
	FindEntityByNameAndType(ctx context.Context, name, entityType, collectionID, userID string) (*model.Entity, error)
	QueryEntities(ctx context.Context, entityType, name, collectionID, userID string, properties map[string]string, offset, limit int) ([]*model.Entity, error)
	CountEntities(ctx context.Context, entityType, name, collectionID, userID string, properties map[string]string) (int64, error)

	CreateRelation(ctx context.Context, relation *model.Relation) error
	UpdateRelation(ctx context.Context, relation *model.Relation) error
	DeleteRelation(ctx context.Context, id string) error
	GetRelation(ctx context.Context, id string) (*model.Relation, error)
	QueryRelations(ctx context.Context, relationType, sourceId, targetId, collectionID, userID string, offset, limit int) ([]*model.Relation, error)
	CountRelations(ctx context.Context, relationType, sourceId, targetId, collectionID, userID string) (int64, error)

	GetGraphStats(ctx context.Context, collectionID, userID string) (*model.GraphStats, error)
	GetRelatedEntities(ctx context.Context, entityId, relationType string) ([]*model.Entity, error)
	GetSubgraph(ctx context.Context, entityID string, depth int, collectionID, userID string) (*model.Subgraph, error)
	GetEntityPaths(ctx context.Context, sourceID, targetID string, maxDepth int, collectionID, userID string) ([]*model.EntityPath, error)

	WithTransaction(ctx context.Context, fn func(txRepo KnowledgeRepository) error) error
	DB() *gorm.DB
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
			"name":         entity.Name,
			"description":  entity.Description,
			"collectionId": entity.CollectionID,
			"createdAt":    entity.CreatedAt.Unix(),
			"updatedAt":    entity.UpdatedAt.Unix(),
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
			"name":         entity.Name,
			"description":  entity.Description,
			"collectionId": entity.CollectionID,
			"updatedAt":    entity.UpdatedAt.Unix(),
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

	if err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("source_id = ? OR target_id = ?", id, id).Delete(&model.Relation{}).Error; err != nil {
			return err
		}
		if err := tx.Delete(&model.Entity{}, "id = ?", id).Error; err != nil {
			return err
		}
		return nil
	}); err != nil {
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

func (r *knowledgeRepository) FindEntityByNameAndType(ctx context.Context, name, entityType, collectionID, userID string) (*model.Entity, error) {
	var entity model.Entity
	query := r.db.WithContext(ctx).Where("name = ? AND type = ?", name, entityType)
	if collectionID != "" {
		query = query.Where("collection_id = ?", collectionID)
	}
	if userID != "" {
		query = query.Where("user_id = ?", userID)
	}
	err := query.First(&entity).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &entity, nil
}

func (r *knowledgeRepository) QueryEntities(ctx context.Context, entityType, name, collectionID, userID string, properties map[string]string, offset, limit int) ([]*model.Entity, error) {
	query := r.db.WithContext(ctx).Model(&model.Entity{})

	if entityType != "" {
		query = query.Where("type = ?", entityType)
	}
	if name != "" {
		query = query.Where("name LIKE ?", "%"+name+"%")
	}
	if collectionID != "" {
		query = query.Where("collection_id = ?", collectionID)
	}
	if userID != "" {
		query = query.Where("user_id = ?", userID)
	}

	var entities []*model.Entity
	q := query.Offset(offset).Order("created_at DESC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	err := q.Find(&entities).Error
	return entities, err
}

func (r *knowledgeRepository) CountEntities(ctx context.Context, entityType, name, collectionID, userID string, properties map[string]string) (int64, error) {
	query := r.db.WithContext(ctx).Model(&model.Entity{})

	if entityType != "" {
		query = query.Where("type = ?", entityType)
	}
	if name != "" {
		query = query.Where("name LIKE ?", "%"+name+"%")
	}
	if collectionID != "" {
		query = query.Where("collection_id = ?", collectionID)
	}
	if userID != "" {
		query = query.Where("user_id = ?", userID)
	}

	var count int64
	err := query.Count(&count).Error
	return count, err
}

func (r *knowledgeRepository) CreateRelation(ctx context.Context, relation *model.Relation) error {
	if relation.ID == "" {
		relation.ID = uuid.NewString()
	}

	// 端点类型必须在写库前解析：读取失败就整次拒绝，
	// 否则 SQL 提交成功、Neo4j 却因为一次读失败永远缺这条边。
	sourceType, targetType, hasEndpoints, err := r.relationEndpoints(ctx, relation.SourceID, relation.TargetID)
	if err != nil {
		return err
	}

	if err := r.db.WithContext(ctx).Create(relation).Error; err != nil {
		return err
	}

	if hasEndpoints {
		properties := relationGraphProperties(relation, true)
		if err := r.neo4jManager.CreateRelationship(ctx, sourceType, relation.SourceID, relation.Type, targetType, relation.TargetID, properties); err != nil {
			logger.Error("关系已写入 PostgreSQL，但同步 Neo4j 失败",
				logger.StringField("id", relation.ID),
				logger.ErrorField(err))
		}
	}

	return nil
}

func (r *knowledgeRepository) UpdateRelation(ctx context.Context, relation *model.Relation) error {
	oldRelation, err := r.GetRelation(ctx, relation.ID)
	if err != nil {
		return err
	}

	var oldSourceType, oldTargetType string
	var hasOldEndpoints bool
	if oldRelation != nil {
		oldSourceType, oldTargetType, hasOldEndpoints, err = r.relationEndpoints(ctx, oldRelation.SourceID, oldRelation.TargetID)
		if err != nil {
			return err
		}
	}
	sourceType, targetType, hasEndpoints, err := r.relationEndpoints(ctx, relation.SourceID, relation.TargetID)
	if err != nil {
		return err
	}

	if err := r.db.WithContext(ctx).Save(relation).Error; err != nil {
		return err
	}

	if hasOldEndpoints {
		if err := r.neo4jManager.DeleteRelationship(ctx, oldSourceType, oldRelation.SourceID, oldRelation.Type, oldTargetType, oldRelation.TargetID); err != nil {
			logger.Error("关系已更新，但从 Neo4j 删除旧关系失败",
				logger.StringField("id", relation.ID),
				logger.ErrorField(err))
		}
	}

	if hasEndpoints {
		properties := relationGraphProperties(relation, false)
		if err := r.neo4jManager.CreateRelationship(ctx, sourceType, relation.SourceID, relation.Type, targetType, relation.TargetID, properties); err != nil {
			logger.Error("关系已更新，但同步 Neo4j 失败",
				logger.StringField("id", relation.ID),
				logger.ErrorField(err))
		}
	}

	return nil
}

func (r *knowledgeRepository) DeleteRelation(ctx context.Context, id string) error {
	relation, err := r.GetRelation(ctx, id)
	if err != nil {
		return err
	}
	if relation == nil {
		return nil
	}

	sourceType, targetType, hasEndpoints, err := r.relationEndpoints(ctx, relation.SourceID, relation.TargetID)
	if err != nil {
		return err
	}

	if err := r.db.WithContext(ctx).Delete(&model.Relation{}, "id = ?", id).Error; err != nil {
		return err
	}

	if hasEndpoints {
		if err := r.neo4jManager.DeleteRelationship(ctx, sourceType, relation.SourceID, relation.Type, targetType, relation.TargetID); err != nil {
			logger.Error("关系已从 PostgreSQL 删除，但 Neo4j 清理失败",
				logger.StringField("id", id),
				logger.ErrorField(err))
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

func (r *knowledgeRepository) QueryRelations(ctx context.Context, relationType, sourceId, targetId, collectionID, userID string, offset, limit int) ([]*model.Relation, error) {
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
	if collectionID != "" {
		query = query.Where("collection_id = ?", collectionID)
	}
	if userID != "" {
		query = query.Where("user_id = ?", userID)
	}

	var relations []*model.Relation
	q := query.Offset(offset).Order("created_at DESC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	err := q.Find(&relations).Error
	return relations, err
}

func (r *knowledgeRepository) CountRelations(ctx context.Context, relationType, sourceId, targetId, collectionID, userID string) (int64, error) {
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
	if collectionID != "" {
		query = query.Where("collection_id = ?", collectionID)
	}
	if userID != "" {
		query = query.Where("user_id = ?", userID)
	}

	var count int64
	err := query.Count(&count).Error
	return count, err
}

func (r *knowledgeRepository) GetGraphStats(ctx context.Context, collectionID, userID string) (*model.GraphStats, error) {
	stats := &model.GraphStats{
		EntityTypeCount:   make(map[string]int64),
		RelationTypeCount: make(map[string]int64),
	}

	entityQuery := r.db.WithContext(ctx).Model(&model.Entity{})
	if collectionID != "" {
		entityQuery = entityQuery.Where("collection_id = ?", collectionID)
	}
	if userID != "" {
		entityQuery = entityQuery.Where("user_id = ?", userID)
	}
	if err := entityQuery.Count(&stats.EntityCount).Error; err != nil {
		return nil, err
	}

	relationQuery := r.db.WithContext(ctx).Model(&model.Relation{})
	if collectionID != "" {
		relationQuery = relationQuery.Where("collection_id = ?", collectionID)
	}
	if userID != "" {
		relationQuery = relationQuery.Where("user_id = ?", userID)
	}
	if err := relationQuery.Count(&stats.RelationCount).Error; err != nil {
		return nil, err
	}

	type TypeCount struct {
		Type  string
		Count int64
	}

	var entityTypeCounts []TypeCount
	etcQuery := r.db.WithContext(ctx).Model(&model.Entity{}).Select("type, count(*) as count").Group("type")
	if collectionID != "" {
		etcQuery = etcQuery.Where("collection_id = ?", collectionID)
	}
	if userID != "" {
		etcQuery = etcQuery.Where("user_id = ?", userID)
	}
	if err := etcQuery.Find(&entityTypeCounts).Error; err != nil {
		logger.Error("获取实体类型统计失败", logger.ErrorField(err))
	} else {
		for _, etc := range entityTypeCounts {
			stats.EntityTypeCount[etc.Type] = etc.Count
		}
	}

	var relationTypeCounts []TypeCount
	rtcQuery := r.db.WithContext(ctx).Model(&model.Relation{}).Select("type, count(*) as count").Group("type")
	if collectionID != "" {
		rtcQuery = rtcQuery.Where("collection_id = ?", collectionID)
	}
	if userID != "" {
		rtcQuery = rtcQuery.Where("user_id = ?", userID)
	}
	if err := rtcQuery.Find(&relationTypeCounts).Error; err != nil {
		logger.Error("获取关系类型统计失败", logger.ErrorField(err))
	} else {
		for _, rtc := range relationTypeCounts {
			stats.RelationTypeCount[rtc.Type] = rtc.Count
		}
	}

	return stats, nil
}

func (r *knowledgeRepository) GetRelatedEntities(ctx context.Context, entityId, relationType string) ([]*model.Entity, error) {
	if r.neo4jManager != nil {
		entity, err := r.GetEntity(ctx, entityId)
		if err != nil {
			return nil, fmt.Errorf("读取实体 %s 失败: %w", entityId, err)
		}
		if entity != nil {
			nodes, err := r.neo4jManager.GetRelatedNodes(ctx, entity.Type, entityId, relationType)
			if err == nil {
				entities, err := hydrateGraphEntities(ctx, nodes, "关联实体", r.GetEntity)
				if err != nil {
					return nil, err
				}
				if len(entities) > 0 {
					return entities, nil
				}
			}
		}
	}

	forwardQuery := r.db.WithContext(ctx).Model(&model.Entity{}).
		Joins("INNER JOIN relations ON entities.id = relations.target_id").
		Where("relations.source_id = ?", entityId)
	if relationType != "" {
		forwardQuery = forwardQuery.Where("relations.type = ?", relationType)
	}

	var entities []*model.Entity
	if err := forwardQuery.Find(&entities).Error; err != nil {
		return nil, err
	}

	reverseQuery := r.db.WithContext(ctx).Model(&model.Entity{}).
		Joins("INNER JOIN relations ON entities.id = relations.source_id").
		Where("relations.target_id = ?", entityId)
	if relationType != "" {
		reverseQuery = reverseQuery.Where("relations.type = ?", relationType)
	}

	var reverseEntities []*model.Entity
	if err := reverseQuery.Find(&reverseEntities).Error; err != nil {
		return nil, err
	}

	entities = append(entities, reverseEntities...)
	return entities, nil
}

func (r *knowledgeRepository) GetSubgraph(ctx context.Context, entityID string, depth int, collectionID, userID string) (*model.Subgraph, error) {
	if depth <= 0 {
		depth = 2
	}
	if depth > 4 {
		depth = 4
	}

	subgraph := &model.Subgraph{}

	if r.neo4jManager != nil {
		entity, err := r.GetEntity(ctx, entityID)
		if err != nil {
			return nil, fmt.Errorf("读取子图根实体 %s 失败: %w", entityID, err)
		}
		if entity != nil {
			neo4jSubgraph, err := r.neo4jManager.GetSubgraph(ctx, entity.Type, entityID, depth)
			if err == nil && neo4jSubgraph != nil {
				nodes, err := hydrateGraphEntities(ctx, neo4jSubgraph.Nodes, "子图节点", r.GetEntity)
				if err != nil {
					return nil, err
				}
				edges, err := hydrateGraphRelations(ctx, neo4jSubgraph.Relationships, "子图关系", r.GetRelation)
				if err != nil {
					return nil, err
				}
				subgraph.Nodes = nodes
				subgraph.Edges = edges
				if len(subgraph.Nodes) > 0 {
					subgraph.NodeCount = len(subgraph.Nodes)
					subgraph.EdgeCount = len(subgraph.Edges)
					return subgraph, nil
				}
			}
		}
	}

	visited := make(map[string]bool)
	entityQueue := []string{entityID}
	visited[entityID] = true

	for d := 0; d < depth; d++ {
		var nextQueue []string
		for _, eid := range entityQueue {
			entity, err := r.GetEntity(ctx, eid)
			if err != nil {
				return nil, fmt.Errorf("子图遍历：读取实体 %s 失败: %w", eid, err)
			}
			if entity == nil {
				continue
			}
			if collectionID != "" && entity.CollectionID != collectionID && entity.CollectionID != "" {
				continue
			}

			found := false
			for _, n := range subgraph.Nodes {
				if n.ID == entity.ID {
					found = true
					break
				}
			}
			if !found {
				subgraph.Nodes = append(subgraph.Nodes, entity)
			}

			related, err := r.GetRelatedEntities(ctx, eid, "")
			if err != nil {
				return nil, err
			}

			for _, relEntity := range related {
				if collectionID != "" && relEntity.CollectionID != collectionID && relEntity.CollectionID != "" {
					continue
				}

				if !visited[relEntity.ID] {
					visited[relEntity.ID] = true
					nextQueue = append(nextQueue, relEntity.ID)
				}

				found := false
				for _, n := range subgraph.Nodes {
					if n.ID == relEntity.ID {
						found = true
						break
					}
				}
				if !found {
					subgraph.Nodes = append(subgraph.Nodes, relEntity)
				}
			}
		}
		entityQueue = nextQueue
	}

	nodeIDs := make(map[string]bool)
	for _, n := range subgraph.Nodes {
		nodeIDs[n.ID] = true
	}

	relationQuery := r.db.WithContext(ctx).Model(&model.Relation{})
	if collectionID != "" {
		relationQuery = relationQuery.Where("collection_id = ? OR collection_id = ''", collectionID)
	}
	var allRelations []*model.Relation
	if err := relationQuery.Find(&allRelations).Error; err != nil {
		return nil, fmt.Errorf("子图遍历：读取关系失败: %w", err)
	}

	for _, rel := range allRelations {
		if nodeIDs[rel.SourceID] && nodeIDs[rel.TargetID] {
			subgraph.Edges = append(subgraph.Edges, rel)
		}
	}

	subgraph.NodeCount = len(subgraph.Nodes)
	subgraph.EdgeCount = len(subgraph.Edges)
	return subgraph, nil
}

func (r *knowledgeRepository) GetEntityPaths(ctx context.Context, sourceID, targetID string, maxDepth int, collectionID, userID string) ([]*model.EntityPath, error) {
	if maxDepth <= 0 {
		maxDepth = 4
	}
	if maxDepth > 6 {
		maxDepth = 6
	}

	var paths []*model.EntityPath

	if r.neo4jManager != nil {
		sourceEntity, sourceErr := r.GetEntity(ctx, sourceID)
		if sourceErr != nil {
			return nil, fmt.Errorf("读取路径起点实体 %s 失败: %w", sourceID, sourceErr)
		}
		targetEntity, targetErr := r.GetEntity(ctx, targetID)
		if targetErr != nil {
			return nil, fmt.Errorf("读取路径终点实体 %s 失败: %w", targetID, targetErr)
		}
		if sourceEntity != nil && targetEntity != nil {
			neo4jPaths, err := r.neo4jManager.GetShortestPath(ctx, sourceEntity.Type, sourceID, targetEntity.Type, targetID, maxDepth)
			if err == nil && len(neo4jPaths) > 0 {
				for _, neo4jPath := range neo4jPaths {
					entities, err := hydrateGraphEntities(ctx, neo4jPath.Nodes, "路径节点", r.GetEntity)
					if err != nil {
						return nil, err
					}
					edges, err := hydrateGraphRelations(ctx, neo4jPath.Relationships, "路径关系", r.GetRelation)
					if err != nil {
						return nil, err
					}
					path := &model.EntityPath{Entities: entities, Edges: edges, Length: len(edges)}
					paths = append(paths, path)
				}
				if len(paths) > 0 {
					return paths, nil
				}
			}
		}
	}

	path, err := r.findPathBFS(ctx, sourceID, targetID, maxDepth, collectionID)
	if err != nil {
		return nil, err
	}
	if path != nil {
		paths = append(paths, path)
	}

	if len(paths) == 0 {
		return []*model.EntityPath{}, nil
	}
	return paths, nil
}

func (r *knowledgeRepository) findPathBFS(ctx context.Context, sourceID, targetID string, maxDepth int, collectionID string) (*model.EntityPath, error) {
	type pathNode struct {
		entityID string
		path     []string
		edges    []string
	}

	visited := make(map[string]bool)
	queue := []pathNode{{entityID: sourceID, path: []string{sourceID}}}
	visited[sourceID] = true

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		if len(current.path)-1 >= maxDepth {
			continue
		}

		related, err := r.GetRelatedEntities(ctx, current.entityID, "")
		if err != nil {
			return nil, fmt.Errorf("路径搜索：读取 %s 的关联实体失败: %w", current.entityID, err)
		}

		for _, relEntity := range related {
			if collectionID != "" && relEntity.CollectionID != collectionID && relEntity.CollectionID != "" {
				continue
			}

			if relEntity.ID == targetID {
				fullPath := append(current.path, relEntity.ID)

				var entities []*model.Entity
				var edges []*model.Relation
				for _, eid := range fullPath {
					e, err := r.GetEntity(ctx, eid)
					if err != nil {
						return nil, fmt.Errorf("路径搜索：读取实体 %s 失败: %w", eid, err)
					}
					if e != nil {
						entities = append(entities, e)
					}
				}

				for i := 0; i < len(fullPath)-1; i++ {
					var rels []*model.Relation
					if err := r.db.WithContext(ctx).Model(&model.Relation{}).
						Where("(source_id = ? AND target_id = ?) OR (source_id = ? AND target_id = ?)",
							fullPath[i], fullPath[i+1], fullPath[i+1], fullPath[i]).
						Find(&rels).Error; err != nil {
						return nil, fmt.Errorf("路径搜索：读取 %s 与 %s 之间的关系失败: %w", fullPath[i], fullPath[i+1], err)
					}
					if len(rels) > 0 {
						edges = append(edges, rels[0])
					}
				}

				return &model.EntityPath{
					Entities: entities,
					Edges:    edges,
					Length:   len(edges),
				}, nil
			}

			if !visited[relEntity.ID] {
				visited[relEntity.ID] = true
				queue = append(queue, pathNode{
					entityID: relEntity.ID,
					path:     append(append([]string{}, current.path...), relEntity.ID),
				})
			}
		}
	}

	return nil, nil
}

func (r *knowledgeRepository) WithTransaction(ctx context.Context, fn func(txRepo KnowledgeRepository) error) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		txRepo := NewKnowledgeRepository(tx, r.neo4jManager)
		return fn(txRepo)
	})
}

func (r *knowledgeRepository) DB() *gorm.DB {
	return r.db
}
