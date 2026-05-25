package tools

import (
	"context"
	"fmt"
	"strings"

	"Logos/pkg/logger"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

type GraphService interface {
	SearchEntities(ctx context.Context, keyword string, entityType *string, collectionID string, page, pageSize int) ([]*GraphEntity, error)
	GetRelatedEntities(ctx context.Context, entityID string, relationType string) ([]*GraphEntity, error)
	GetSubgraph(ctx context.Context, entityID string, depth int, collectionID string) (*GraphSubgraph, error)
	GetEntityPaths(ctx context.Context, sourceID, targetID string, maxDepth int, collectionID string) ([]*GraphEntityPath, error)
	GetGraphStats(ctx context.Context, collectionID string) (*GraphStatsInfo, error)
}

type GraphEntity struct {
	ID           string            `json:"id"`
	Type         string            `json:"type"`
	Name         string            `json:"name"`
	Properties   map[string]string `json:"properties"`
	Description  string            `json:"description,omitempty"`
	CollectionID string            `json:"collection_id,omitempty"`
	Color        string            `json:"color,omitempty"`
}

type GraphRelation struct {
	ID           string            `json:"id"`
	Type         string            `json:"type"`
	SourceID     string            `json:"source_id"`
	TargetID     string            `json:"target_id"`
	Properties   map[string]string `json:"properties"`
	Description  string            `json:"description,omitempty"`
	CollectionID string            `json:"collection_id,omitempty"`
}

type GraphSubgraph struct {
	Nodes     []*GraphEntity   `json:"nodes"`
	Edges     []*GraphRelation `json:"edges"`
	NodeCount int              `json:"node_count"`
	EdgeCount int              `json:"edge_count"`
}

type GraphEntityPath struct {
	Entities []*GraphEntity   `json:"entities"`
	Edges    []*GraphRelation `json:"edges"`
	Length   int              `json:"length"`
}

type GraphStatsInfo struct {
	EntityCount       int64            `json:"entity_count"`
	RelationCount     int64            `json:"relation_count"`
	EntityTypeCount   map[string]int64 `json:"entity_type_count"`
	RelationTypeCount map[string]int64 `json:"relation_type_count"`
}

type GraphSearchInput struct {
	Keyword      string `json:"keyword" jsonschema:"description=搜索关键词,用于在知识图谱中搜索实体名称和描述"`
	EntityType   string `json:"entity_type,omitempty" jsonschema:"description=实体类型过滤(如person,organization,concept等),为空则搜索所有类型"`
	CollectionID string `json:"collection_id,omitempty" jsonschema:"description=集合ID,限定搜索范围,为空则搜索所有集合"`
	TopK         int    `json:"top_k,omitempty" jsonschema:"description=返回结果数量,默认10,最大30"`
}

type GraphExploreInput struct {
	EntityID     string `json:"entity_id" jsonschema:"description=要探索的中心实体ID"`
	Depth        int    `json:"depth,omitempty" jsonschema:"description=探索深度(1-3),默认2,越大探索范围越广"`
	CollectionID string `json:"collection_id,omitempty" jsonschema:"description=集合ID,限定探索范围"`
}

type GraphPathInput struct {
	SourceID     string `json:"source_id" jsonschema:"description=起点实体ID"`
	TargetID     string `json:"target_id" jsonschema:"description=终点实体ID"`
	MaxDepth     int    `json:"max_depth,omitempty" jsonschema:"description=最大搜索深度,默认4"`
	CollectionID string `json:"collection_id,omitempty" jsonschema:"description=集合ID,限定搜索范围"`
}

func NewGraphSearchTool(svc GraphService, defaultCollectionID string) (tool.InvokableTool, error) {
	t, err := utils.InferTool("graph_search",
		"知识图谱搜索工具：在知识图谱中搜索实体。"+
			"当你需要查找某个概念、人物、组织、事件等实体时使用此工具。"+
			"返回匹配的实体列表，包含名称、类型、描述和属性。"+
			"如果需要了解实体之间的关系，请使用 graph_explore 工具。",
		func(ctx context.Context, input *GraphSearchInput) (string, error) {
			topK := input.TopK
			if topK <= 0 {
				topK = 10
			}
			if topK > 30 {
				topK = 30
			}

			collectionID := input.CollectionID
			if collectionID == "" {
				collectionID = defaultCollectionID
			}

			logger.Info("GraphSearchTool 调用",
				logger.StringField("keyword", input.Keyword),
				logger.StringField("type", input.EntityType),
				logger.StringField("collectionId", collectionID))

			var entityType *string
			if input.EntityType != "" {
				entityType = &input.EntityType
			}

			entities, err := svc.SearchEntities(ctx, input.Keyword, entityType, collectionID, 1, topK)
			if err != nil {
				logger.Error("图谱搜索失败", logger.ErrorField(err))
				return fmt.Sprintf("知识图谱搜索失败: %s", err.Error()), nil
			}

			if len(entities) == 0 {
				return "在知识图谱中未找到匹配的实体。可以尝试使用不同的关键词，或检查实体类型是否正确。", nil
			}

			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("=== 知识图谱搜索结果 ===\n"))
			sb.WriteString(fmt.Sprintf("共找到 %d 个实体\n\n", len(entities)))

			for i, e := range entities {
				sb.WriteString(fmt.Sprintf("--- 实体 %d ---\n", i+1))
				sb.WriteString(fmt.Sprintf("ID: %s\n", e.ID))
				sb.WriteString(fmt.Sprintf("名称: %s\n", e.Name))
				sb.WriteString(fmt.Sprintf("类型: %s\n", e.Type))
				if e.Color != "" {
					sb.WriteString(fmt.Sprintf("颜色: %s\n", e.Color))
				}
				if e.Description != "" {
					sb.WriteString(fmt.Sprintf("描述: %s\n", e.Description))
				}
				if len(e.Properties) > 0 {
					sb.WriteString("属性:\n")
					for k, v := range e.Properties {
						sb.WriteString(fmt.Sprintf("  %s: %s\n", k, v))
					}
				}
				sb.WriteString("\n")
			}

			sb.WriteString("提示: 使用 graph_explore 可以探索实体的关联关系，使用 graph_path 可以查找两个实体之间的路径。")
			return sb.String(), nil
		})
	if err != nil {
		return nil, fmt.Errorf("创建 GraphSearchTool 失败: %w", err)
	}
	return t, nil
}

func NewGraphExploreTool(svc GraphService, defaultCollectionID string) (tool.InvokableTool, error) {
	t, err := utils.InferTool("graph_explore",
		"知识图谱探索工具：从指定实体出发，探索其关联实体和关系。"+
			"当你想了解某个实体的上下文关系、邻居节点时使用此工具。"+
			"返回以指定实体为中心的子图，包含关联实体和关系。",
		func(ctx context.Context, input *GraphExploreInput) (string, error) {
			depth := input.Depth
			if depth <= 0 {
				depth = 2
			}
			if depth > 3 {
				depth = 3
			}

			collectionID := input.CollectionID
			if collectionID == "" {
				collectionID = defaultCollectionID
			}

			logger.Info("GraphExploreTool 调用",
				logger.StringField("entityId", input.EntityID),
				logger.IntField("depth", depth),
				logger.StringField("collectionId", collectionID))

			subgraph, err := svc.GetSubgraph(ctx, input.EntityID, depth, collectionID)
			if err != nil {
				logger.Error("图谱探索失败", logger.ErrorField(err))
				return fmt.Sprintf("知识图谱探索失败: %s", err.Error()), nil
			}

			if subgraph == nil || len(subgraph.Nodes) == 0 {
				return "未找到该实体的关联信息。请确认实体ID是否正确。", nil
			}

			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("=== 知识图谱探索结果 ===\n"))
			sb.WriteString(fmt.Sprintf("节点数: %d, 关系数: %d\n\n", subgraph.NodeCount, subgraph.EdgeCount))

			sb.WriteString("关联实体:\n")
			for i, n := range subgraph.Nodes {
				sb.WriteString(fmt.Sprintf("  %d. [%s] %s (ID: %s)\n", i+1, n.Type, n.Name, n.ID))
				if n.Description != "" {
					sb.WriteString(fmt.Sprintf("     描述: %s\n", n.Description))
				}
			}

			sb.WriteString("\n关系:\n")
			for i, e := range subgraph.Edges {
				sb.WriteString(fmt.Sprintf("  %d. %s → [%s] → %s\n", i+1, e.SourceID, e.Type, e.TargetID))
				if e.Description != "" {
					sb.WriteString(fmt.Sprintf("     描述: %s\n", e.Description))
				}
			}

			return sb.String(), nil
		})
	if err != nil {
		return nil, fmt.Errorf("创建 GraphExploreTool 失败: %w", err)
	}
	return t, nil
}

func NewGraphPathTool(svc GraphService, defaultCollectionID string) (tool.InvokableTool, error) {
	t, err := utils.InferTool("graph_path",
		"知识图谱路径查找工具：查找两个实体之间的关联路径。"+
			"当你想了解两个实体如何关联、经过哪些中间节点时使用此工具。"+
			"返回实体间的最短路径，包含经过的所有实体和关系。",
		func(ctx context.Context, input *GraphPathInput) (string, error) {
			maxDepth := input.MaxDepth
			if maxDepth <= 0 {
				maxDepth = 4
			}
			if maxDepth > 6 {
				maxDepth = 6
			}

			collectionID := input.CollectionID
			if collectionID == "" {
				collectionID = defaultCollectionID
			}

			logger.Info("GraphPathTool 调用",
				logger.StringField("sourceId", input.SourceID),
				logger.StringField("targetId", input.TargetID),
				logger.IntField("maxDepth", maxDepth),
				logger.StringField("collectionId", collectionID))

			paths, err := svc.GetEntityPaths(ctx, input.SourceID, input.TargetID, maxDepth, collectionID)
			if err != nil {
				logger.Error("路径查找失败", logger.ErrorField(err))
				return fmt.Sprintf("路径查找失败: %s", err.Error()), nil
			}

			if len(paths) == 0 {
				return "未找到两个实体之间的关联路径。它们可能没有直接或间接的关系。", nil
			}

			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("=== 知识图谱路径查找结果 ===\n"))
			sb.WriteString(fmt.Sprintf("共找到 %d 条路径\n\n", len(paths)))

			for i, path := range paths {
				sb.WriteString(fmt.Sprintf("--- 路径 %d (长度: %d) ---\n", i+1, path.Length))
				for j, e := range path.Entities {
					if j > 0 {
						if j-1 < len(path.Edges) {
							sb.WriteString(fmt.Sprintf(" --[%s]--> ", path.Edges[j-1].Type))
						} else {
							sb.WriteString(" ---> ")
						}
					}
					sb.WriteString(fmt.Sprintf("[%s] %s", e.Type, e.Name))
				}
				sb.WriteString("\n\n")
			}

			return sb.String(), nil
		})
	if err != nil {
		return nil, fmt.Errorf("创建 GraphPathTool 失败: %w", err)
	}
	return t, nil
}

func BuildGraphSearchTools(svc GraphService, defaultCollectionID string) []tool.BaseTool {
	var tools []tool.BaseTool

	searchTool, err := NewGraphSearchTool(svc, defaultCollectionID)
	if err != nil {
		logger.Warn("创建图谱搜索工具失败", logger.ErrorField(err))
	} else {
		tools = append(tools, searchTool)
	}

	exploreTool, err := NewGraphExploreTool(svc, defaultCollectionID)
	if err != nil {
		logger.Warn("创建图谱探索工具失败", logger.ErrorField(err))
	} else {
		tools = append(tools, exploreTool)
	}

	pathTool, err := NewGraphPathTool(svc, defaultCollectionID)
	if err != nil {
		logger.Warn("创建图谱路径工具失败", logger.ErrorField(err))
	} else {
		tools = append(tools, pathTool)
	}

	return tools
}
