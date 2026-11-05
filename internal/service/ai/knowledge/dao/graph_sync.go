package dao

import (
	"context"
	"fmt"
	"strings"

	"Logos/internal/service/ai/knowledge/model"
	"Logos/pkg/logger"
)

type entityLookup func(ctx context.Context, id string) (*model.Entity, error)
type relationLookup func(ctx context.Context, id string) (*model.Relation, error)

// hydrateGraphEntities 把 Neo4j 返回的节点补全成数据库实体。
// 查询失败必须上抛：返回「半张子图 + nil」等于告诉调用方「图就长这样」。
func hydrateGraphEntities(ctx context.Context, nodes []map[string]any, source string, lookup entityLookup) ([]*model.Entity, error) {
	entities := make([]*model.Entity, 0, len(nodes))
	invalid := 0
	deleted := 0
	for _, nodeProps := range nodes {
		id, ok := nodeProps["id"].(string)
		if !ok || strings.TrimSpace(id) == "" {
			invalid++
			continue
		}
		entity, err := lookup(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("%s：读取实体 %s 失败: %w", source, id, err)
		}
		if entity == nil {
			deleted++
			continue
		}
		entities = append(entities, entity)
	}
	if invalid > 0 {
		logger.Warn("Neo4j 节点缺少有效 id，已跳过",
			logger.StringField("source", source),
			logger.IntField("count", invalid))
	}
	if deleted > 0 {
		logger.Warn("Neo4j 节点在 PostgreSQL 已不存在，已跳过",
			logger.StringField("source", source),
			logger.IntField("count", deleted))
	}
	return entities, nil
}

// hydrateGraphRelations 与 hydrateGraphEntities 同规则：读失败上抛，已删除的边跳过并计数。
func hydrateGraphRelations(ctx context.Context, relations []map[string]any, source string, lookup relationLookup) ([]*model.Relation, error) {
	result := make([]*model.Relation, 0, len(relations))
	invalid := 0
	deleted := 0
	for _, relProps := range relations {
		id, ok := relProps["id"].(string)
		if !ok || strings.TrimSpace(id) == "" {
			invalid++
			continue
		}
		relation, err := lookup(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("%s：读取关系 %s 失败: %w", source, id, err)
		}
		if relation == nil {
			deleted++
			continue
		}
		result = append(result, relation)
	}
	if invalid > 0 {
		logger.Warn("Neo4j 关系缺少有效 id，已跳过",
			logger.StringField("source", source),
			logger.IntField("count", invalid))
	}
	if deleted > 0 {
		logger.Warn("Neo4j 关系在 PostgreSQL 已不存在，已跳过",
			logger.StringField("source", source),
			logger.IntField("count", deleted))
	}
	return result, nil
}

// resolveRelationEndpoints 解析关系两端的实体类型，供 Neo4j 同步使用。
// ok=false 表示端点确实不存在（例如导入顺序导致），此时保留关系记录但不写图；
// error 表示数据库读取失败，调用方必须在写库前中止——
// 把「查失败」当成「不存在」，只会留下一条永远同步不进图的边。
func resolveRelationEndpoints(ctx context.Context, sourceID, targetID string, lookup entityLookup) (sourceType, targetType string, ok bool, err error) {
	source, err := lookup(ctx, sourceID)
	if err != nil {
		return "", "", false, fmt.Errorf("读取关系源实体 %s 失败: %w", sourceID, err)
	}
	target, err := lookup(ctx, targetID)
	if err != nil {
		return "", "", false, fmt.Errorf("读取关系目标实体 %s 失败: %w", targetID, err)
	}
	if source == nil || target == nil {
		logger.Warn("关系端点实体不存在，跳过 Neo4j 同步",
			logger.StringField("sourceId", sourceID),
			logger.StringField("targetId", targetID))
		return "", "", false, nil
	}
	return source.Type, target.Type, true, nil
}

// relationEndpoints 在写库前解析端点；未启用 Neo4j 时不做任何查询。
func (r *knowledgeRepository) relationEndpoints(ctx context.Context, sourceID, targetID string) (string, string, bool, error) {
	if r.neo4jManager == nil {
		return "", "", false, nil
	}
	return resolveRelationEndpoints(ctx, sourceID, targetID, r.GetEntity)
}

// relationGraphProperties 统一创建与更新时的图属性，避免两条路径字段各写一份。
func relationGraphProperties(relation *model.Relation, withCreatedAt bool) map[string]interface{} {
	properties := map[string]interface{}{
		"description":  relation.Description,
		"collectionId": relation.CollectionID,
		"updatedAt":    relation.UpdatedAt.Unix(),
	}
	if withCreatedAt {
		properties["createdAt"] = relation.CreatedAt.Unix()
	}
	for key, value := range relation.Properties {
		properties[key] = value
	}
	return properties
}
