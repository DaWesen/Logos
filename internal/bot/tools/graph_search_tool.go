package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"Logos/pkg/logger"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

const (
	// graphOutputRuneBudget 单次图谱工具输出的字符预算。
	// 30 个实体、每个都带完整描述与属性时，一次调用就能吃掉整个上下文窗口；
	// 工具返回值是 prompt 的一部分，必须有全局预算而不是只限制条数。
	graphOutputRuneBudget = 6000
	// graphFieldRuneBudget 单条描述/属性值的字符上限。
	graphFieldRuneBudget = 300
	// graphPathDisplayLimit 路径结果最多展示条数（路径条数由服务端决定，不可信）。
	graphPathDisplayLimit = 10
	// graphMaxPropertiesPerEntity 单个实体最多展示的属性条数：
	// 只限制每条属性值长度不够，属性数量本身也可能是外部数据决定的。
	graphMaxPropertiesPerEntity = 8
)

// truncateForPrompt 按 rune 截断，避免把中文切成非法 UTF-8。
func truncateForPrompt(s string, maxRunes int) string {
	if maxRunes <= 0 || utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	r := []rune(s)
	return string(r[:maxRunes]) + "…"
}

// sortedPropertyKeys 属性是 map，直接遍历会让同一实体的输出顺序每次都不同，
// 既影响 prompt 缓存命中，也让「同一问题两次回答不一致」变得无法复现。
func sortedPropertyKeys(props map[string]string) []string {
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func buildEntityBlock(e *GraphEntity) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("ID: %s\n", e.ID))
	sb.WriteString(fmt.Sprintf("名称: %s\n", e.Name))
	sb.WriteString(fmt.Sprintf("类型: %s\n", e.Type))
	if e.Color != "" {
		sb.WriteString(fmt.Sprintf("颜色: %s\n", e.Color))
	}
	if e.Description != "" {
		sb.WriteString(fmt.Sprintf("描述: %s\n", truncateForPrompt(e.Description, graphFieldRuneBudget)))
	}
	if len(e.Properties) > 0 {
		sb.WriteString("属性:\n")
		keys := sortedPropertyKeys(e.Properties)
		limit := len(keys)
		if limit > graphMaxPropertiesPerEntity {
			limit = graphMaxPropertiesPerEntity
		}
		for _, k := range keys[:limit] {
			sb.WriteString(fmt.Sprintf("  %s: %s\n", k, truncateForPrompt(e.Properties[k], graphFieldRuneBudget)))
		}
		if limit < len(keys) {
			sb.WriteString(fmt.Sprintf("  （其余 %d 个属性已省略）\n", len(keys)-limit))
		}
	}
	sb.WriteString("\n")
	return sb.String()
}

// graphFooterReserve 给「已截断」提示和结尾提示预留的字符数，
// 避免正文刚好用满预算后，尾巴再把总量顶出上限。
const graphFooterReserve = 160

func formatGraphSearchResult(entities []*GraphEntity) string {
	var header strings.Builder
	fmt.Fprintf(&header, "=== 知识图谱搜索结果 ===\n共找到 %d 个实体\n\n", len(entities))

	var body strings.Builder
	used := utf8.RuneCountInString(header.String())
	shown := 0
	for _, e := range entities {
		block := fmt.Sprintf("--- 实体 %d ---\n", shown+1) + buildEntityBlock(e)
		blockLen := utf8.RuneCountInString(block)
		if shown > 0 && used+blockLen+graphFooterReserve > graphOutputRuneBudget {
			break
		}
		body.WriteString(block)
		used += blockLen
		shown++
	}

	var sb strings.Builder
	sb.WriteString(header.String())
	sb.WriteString(body.String())
	if shown < len(entities) {
		fmt.Fprintf(&sb, "（结果已截断，仅展示前 %d / %d 个实体；可缩小关键词或指定 entity_type 后再查）\n", shown, len(entities))
	}
	sb.WriteString("提示: 使用 graph_explore 可以探索实体的关联关系，使用 graph_path 可以查找两个实体之间的路径。")
	return sb.String()
}

func formatGraphExploreResult(sub *GraphSubgraph) string {
	var sb strings.Builder
	sb.WriteString("=== 知识图谱探索结果 ===\n")
	// 用实际切片长度，而不是服务端给的 NodeCount/EdgeCount：后者可能统计的是
	// 整库或截断前的数量，和实际列出的条目对不上，模型会误判「还有内容没看到」。
	fmt.Fprintf(&sb, "节点数: %d, 关系数: %d\n\n", len(sub.Nodes), len(sub.Edges))
	used := utf8.RuneCountInString(sb.String())

	sb.WriteString("关联实体:\n")
	shownNodes := 0
	for i, n := range sub.Nodes {
		line := fmt.Sprintf("  %d. [%s] %s (ID: %s)\n", i+1, n.Type, n.Name, n.ID)
		if n.Description != "" {
			line += fmt.Sprintf("     描述: %s\n", truncateForPrompt(n.Description, graphFieldRuneBudget))
		}
		lineLen := utf8.RuneCountInString(line)
		if shownNodes > 0 && used+lineLen+graphFooterReserve > graphOutputRuneBudget {
			break
		}
		sb.WriteString(line)
		used += lineLen
		shownNodes++
	}
	if shownNodes < len(sub.Nodes) {
		fmt.Fprintf(&sb, "（仅展示前 %d / %d 个节点）\n", shownNodes, len(sub.Nodes))
	}

	sb.WriteString("\n关系:\n")
	shownEdges := 0
	for i, e := range sub.Edges {
		line := fmt.Sprintf("  %d. %s → [%s] → %s\n", i+1, e.SourceID, e.Type, e.TargetID)
		if e.Description != "" {
			line += fmt.Sprintf("     描述: %s\n", truncateForPrompt(e.Description, graphFieldRuneBudget))
		}
		lineLen := utf8.RuneCountInString(line)
		if shownEdges > 0 && used+lineLen > graphOutputRuneBudget {
			break
		}
		sb.WriteString(line)
		used += lineLen
		shownEdges++
	}
	if shownEdges < len(sub.Edges) {
		fmt.Fprintf(&sb, "（仅展示前 %d / %d 条关系）\n", shownEdges, len(sub.Edges))
	}

	return sb.String()
}

func formatGraphPaths(paths []*GraphEntityPath) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "=== 知识图谱路径查找结果 ===\n共找到 %d 条路径\n\n", len(paths))

	shown := len(paths)
	if shown > graphPathDisplayLimit {
		shown = graphPathDisplayLimit
	}

	for i := 0; i < shown; i++ {
		path := paths[i]
		fmt.Fprintf(&sb, "--- 路径 %d (长度: %d) ---\n", i+1, path.Length)
		for j, e := range path.Entities {
			if j > 0 {
				if j-1 < len(path.Edges) {
					sb.WriteString(fmt.Sprintf(" --[%s]--> ", path.Edges[j-1].Type))
				} else {
					sb.WriteString(" ---> ")
				}
			}
			sb.WriteString(fmt.Sprintf("[%s] %s", e.Type, truncateForPrompt(e.Name, graphFieldRuneBudget)))
		}
		sb.WriteString("\n\n")
	}

	if shown < len(paths) {
		fmt.Fprintf(&sb, "（仅展示前 %d / %d 条路径）\n", shown, len(paths))
	}
	return sb.String()
}

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
				// 失败不能伪装成「没有匹配」的文本：模型会把连接错误当事实转述，
				// 或者换个关键词继续重试，把一次服务故障放大成多轮 LLM 调用。
				return "", fmt.Errorf("图谱搜索失败: %w", err)
			}

			if len(entities) == 0 {
				return "在知识图谱中未找到匹配的实体。可以尝试使用不同的关键词，或检查实体类型是否正确。", nil
			}

			return formatGraphSearchResult(entities), nil
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
				return "", fmt.Errorf("图谱探索失败: %w", err)
			}

			if subgraph == nil || len(subgraph.Nodes) == 0 {
				return "未找到该实体的关联信息。请确认实体ID是否正确。", nil
			}

			return formatGraphExploreResult(subgraph), nil
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
				return "", fmt.Errorf("路径查找失败: %w", err)
			}

			if len(paths) == 0 {
				return "未找到两个实体之间的关联路径。它们可能没有直接或间接的关系。", nil
			}

			return formatGraphPaths(paths), nil
		})
	if err != nil {
		return nil, fmt.Errorf("创建 GraphPathTool 失败: %w", err)
	}
	return t, nil
}

func BuildGraphSearchTools(svc GraphService, defaultCollectionID string) []tool.BaseTool {
	var tools []tool.BaseTool
	inv := getGraphSearchInvoker()
	// 图谱检索是只读的：可以同参合并、重试和熔断，但不做缓存。
	// 图数据会被 graph_write 修改，缓存 30s 会让「刚写入就搜索」看到旧结果。
	policy := ToolPolicy{ReadOnly: true, Retryable: true, Breaker: true}

	searchTool, err := NewGraphSearchTool(svc, defaultCollectionID)
	if err != nil {
		logger.Warn("创建图谱搜索工具失败", logger.ErrorField(err))
	} else {
		tools = append(tools, inv.Wrap(searchTool, policy))
	}

	exploreTool, err := NewGraphExploreTool(svc, defaultCollectionID)
	if err != nil {
		logger.Warn("创建图谱探索工具失败", logger.ErrorField(err))
	} else {
		tools = append(tools, inv.Wrap(exploreTool, policy))
	}

	pathTool, err := NewGraphPathTool(svc, defaultCollectionID)
	if err != nil {
		logger.Warn("创建图谱路径工具失败", logger.ErrorField(err))
	} else {
		tools = append(tools, inv.Wrap(pathTool, policy))
	}

	return tools
}

var (
	graphSearchInvokerOnce sync.Once
	graphSearchInvoker     *Invoker
)

func getGraphSearchInvoker() *Invoker {
	graphSearchInvokerOnce.Do(func() {
		graphSearchInvoker = NewInvoker(0)
	})
	return graphSearchInvoker
}
