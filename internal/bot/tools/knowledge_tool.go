package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"Logos/pkg/logger"
	"Logos/pkg/strutil"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

type KnowledgeSearchService interface {
	SearchVector(ctx context.Context, collectionIDs []string, query string, topK int) ([]*KnowledgeSearchResult, error)
	SearchKeyword(ctx context.Context, query string, topK int) ([]*KnowledgeSearchResult, error)
	ListCollections(ctx context.Context) ([]*CollectionInfo, error)
}

type KnowledgeSearchResult struct {
	ID       string            `json:"id"`
	Title    string            `json:"title"`
	Content  string            `json:"content"`
	Score    float64           `json:"score"`
	Source   string            `json:"source"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

type CollectionInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Size int64  `json:"size"`
}

type KnowledgeSearchInput struct {
	Queries       []string `json:"queries" jsonschema:"description=语义搜索查询列表(1-3个),用自然语言描述你想查找的信息"`
	CollectionIDs []string `json:"collection_ids,omitempty" jsonschema:"description=要搜索的知识库/集合ID列表,为空则搜索所有可用知识库"`
	TopK          int      `json:"top_k,omitempty" jsonschema:"description=返回结果数量,默认5,最大20"`
}

type GrepChunksInput struct {
	Patterns      []string `json:"patterns" jsonschema:"description=关键词搜索模式列表(1-3个),每个应为1-3个核心词"`
	CollectionIDs []string `json:"collection_ids,omitempty" jsonschema:"description=要搜索的知识库/集合ID列表,为空则搜索所有可用知识库"`
	TopK          int      `json:"top_k,omitempty" jsonschema:"description=返回结果数量,默认10,最大30"`
}

func NewKnowledgeSearchTool(svc KnowledgeSearchService) (tool.InvokableTool, error) {
	t, err := utils.InferTool("knowledge_search",
		"知识库语义搜索工具：通过语义理解从知识库中检索与问题相关的文档片段。"+
			"当用户的问题涉及知识库中的专业内容、文档信息、特定领域知识时使用此工具。"+
			"支持多个查询以获取更全面的结果。返回按相关性排序的文档片段。",
		func(ctx context.Context, input *KnowledgeSearchInput) (string, error) {
			topK := input.TopK
			if topK <= 0 {
				topK = 5
			}
			if topK > 20 {
				topK = 20
			}

			queries := input.Queries
			if len(queries) > 3 {
				queries = queries[:3]
			}

			logger.Info("KnowledgeSearchTool 调用",
				logger.AnyField("queries", queries),
				logger.AnyField("collection_ids", input.CollectionIDs),
				logger.IntField("top_k", topK))

			type queryResult struct {
				results []*KnowledgeSearchResult
				err     error
			}
			resultCh := make(chan queryResult, len(queries))
			var wg sync.WaitGroup

			for _, query := range queries {
				if query == "" {
					continue
				}
				wg.Add(1)
				go func(q string) {
					defer wg.Done()
					results, err := svc.SearchVector(ctx, input.CollectionIDs, q, topK)
					resultCh <- queryResult{results: results, err: err}
				}(query)
			}

			go func() {
				wg.Wait()
				close(resultCh)
			}()

			var allResults []*KnowledgeSearchResult
			seen := make(map[string]bool)

			for qr := range resultCh {
				if qr.err != nil {
					logger.Warn("语义搜索失败", logger.ErrorField(qr.err))
					continue
				}
				for _, r := range qr.results {
					if !seen[r.ID] {
						seen[r.ID] = true
						allResults = append(allResults, r)
					}
				}
			}

			if len(allResults) == 0 {
				return "未找到与查询相关的知识库内容。请尝试换一种方式描述你的问题，或使用 grep_chunks 工具进行关键词搜索。", nil
			}

			return formatSearchResults(allResults, "语义搜索"), nil
		})
	if err != nil {
		return nil, fmt.Errorf("创建 KnowledgeSearchTool 失败: %w", err)
	}
	return t, nil
}

func NewGrepChunksTool(svc KnowledgeSearchService) (tool.InvokableTool, error) {
	t, err := utils.InferTool("grep_chunks",
		"知识库关键词搜索工具：通过精确关键词匹配从知识库中查找包含指定词语的文档片段。"+
			"当你知道确切的术语、名称、编号等需要精确匹配时使用此工具。"+
			"每个搜索模式应为1-3个核心关键词，不要使用完整句子。",
		func(ctx context.Context, input *GrepChunksInput) (string, error) {
			topK := input.TopK
			if topK <= 0 {
				topK = 10
			}
			if topK > 30 {
				topK = 30
			}

			patterns := input.Patterns
			if len(patterns) > 3 {
				patterns = patterns[:3]
			}

			logger.Info("GrepChunksTool 调用",
				logger.AnyField("patterns", patterns),
				logger.AnyField("collection_ids", input.CollectionIDs),
				logger.IntField("top_k", topK))

			type patternResult struct {
				results []*KnowledgeSearchResult
				err     error
			}
			resultCh := make(chan patternResult, len(patterns))
			var wg sync.WaitGroup

			for _, pattern := range patterns {
				if pattern == "" {
					continue
				}
				wg.Add(1)
				go func(p string) {
					defer wg.Done()
					results, err := svc.SearchKeyword(ctx, p, topK)
					resultCh <- patternResult{results: results, err: err}
				}(pattern)
			}

			go func() {
				wg.Wait()
				close(resultCh)
			}()

			var allResults []*KnowledgeSearchResult
			seen := make(map[string]bool)

			for pr := range resultCh {
				if pr.err != nil {
					logger.Warn("关键词搜索失败", logger.ErrorField(pr.err))
					continue
				}
				for _, r := range pr.results {
					if !seen[r.ID] {
						seen[r.ID] = true
						r.Source = "keyword"
						allResults = append(allResults, r)
					}
				}
			}

			if len(allResults) == 0 {
				return "未找到包含指定关键词的文档片段。请尝试使用不同的关键词，或使用 knowledge_search 工具进行语义搜索。", nil
			}

			return formatSearchResults(allResults, "关键词搜索"), nil
		})
	if err != nil {
		return nil, fmt.Errorf("创建 GrepChunksTool 失败: %w", err)
	}
	return t, nil
}

func BuildKnowledgeTools(svc KnowledgeSearchService) []tool.BaseTool {
	var tools []tool.BaseTool

	searchTool, err := NewKnowledgeSearchTool(svc)
	if err != nil {
		logger.Warn("创建知识库语义搜索工具失败", logger.ErrorField(err))
	} else {
		tools = append(tools, searchTool)
	}

	grepTool, err := NewGrepChunksTool(svc)
	if err != nil {
		logger.Warn("创建知识库关键词搜索工具失败", logger.ErrorField(err))
	} else {
		tools = append(tools, grepTool)
	}

	return tools
}

func formatSearchResults(results []*KnowledgeSearchResult, searchType string) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("=== 知识库搜索结果 (%s) ===\n", searchType))
	sb.WriteString(fmt.Sprintf("共找到 %d 条相关结果\n\n", len(results)))

	for i, r := range results {
		relevance := getRelevanceLevel(r.Score)
		sb.WriteString(fmt.Sprintf("--- 结果 %d [%s | 相关度: %s | 得分: %.4f] ---\n", i+1, r.Source, relevance, r.Score))
		if r.Title != "" {
			sb.WriteString(fmt.Sprintf("标题: %s\n", r.Title))
		}
		content := strutil.TruncateByRunes(r.Content, 400)
		sb.WriteString(fmt.Sprintf("内容:\n%s\n", content))
		if len(r.Metadata) > 0 {
			if collID, ok := r.Metadata["collection_id"]; ok {
				sb.WriteString(fmt.Sprintf("来源集合: %s\n", collID))
			}
		}
		sb.WriteString("\n")
	}

	sb.WriteString("提示: 如果以上结果不够精确，可以尝试使用另一个搜索工具（knowledge_search 或 grep_chunks）获取更多信息。")

	return sb.String()
}

func getRelevanceLevel(score float64) string {
	switch {
	case score >= 0.8:
		return "高"
	case score >= 0.6:
		return "中"
	case score >= 0.4:
		return "低"
	default:
		return "弱"
	}
}

func FormatKnowledgeBaseList(collections []*CollectionInfo) string {
	if len(collections) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("\n以下知识库已绑定到当前对话，你可以通过工具搜索其中的内容：\n\n")
	for i, coll := range collections {
		sb.WriteString(fmt.Sprintf("%d. **%s** (collection_id: `%s`)\n", i+1, coll.Name, coll.ID))
		sb.WriteString(fmt.Sprintf("   - 文档数量: %d\n", coll.Size))
	}
	sb.WriteString("\n搜索建议：\n")
	sb.WriteString("- 使用 `knowledge_search` 进行语义搜索（适合概念性、理解性问题）\n")
	sb.WriteString("- 使用 `grep_chunks` 进行关键词搜索（适合精确术语、名称查找）\n")
	sb.WriteString("- 可以多次搜索以获取更全面的信息\n")
	return sb.String()
}
