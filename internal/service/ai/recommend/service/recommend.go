package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"Logos/internal/service/ai/recommend/dao"
	"Logos/internal/service/ai/recommend/model"
	"Logos/pkg/cache"
	"Logos/pkg/eino"
	"Logos/pkg/logger"
	"Logos/pkg/obs"
	"Logos/pkg/pagination"
	"Logos/pkg/singleflight"
)

type VectorService interface {
	SearchSimilar(ctx context.Context, text string, topK int) ([]string, error)
}

type KnowledgeService interface {
	SearchEntities(ctx context.Context, query string) ([]string, error)
}

// chatGenerator 只依赖「一次问答」这个能力，而不是整个 *eino.EinoManager：
// 生成标题的逻辑不需要嵌入、流式或治理细节，接口收窄后在测试里可以直接替身。
type chatGenerator interface {
	Chat(ctx context.Context, messages []string) (string, error)
}

type RecommendService interface {
	GetRecommendations(ctx context.Context, userID int64, recommendType string, limit int, reqContext map[string]string) ([]*model.RecommendationItem, int64, error)
	GetRelatedRecommendations(ctx context.Context, entityID string, recommendType string, limit int) ([]*model.RecommendationItem, int64, error)
	SubmitFeedback(ctx context.Context, itemID string, userID int64, action string, timestamp int64) error
	GetRecommendationHistory(ctx context.Context, userID int64, page, pageSize int) ([]*model.RecommendationHistory, int64, error)
	BatchGetRecommendations(ctx context.Context, userIDs []int64, recommendType string, limit int) (map[int64][]*model.RecommendationItem, error)
}

type recommendServiceImpl struct {
	recommendRepo    dao.RecommendRepository
	einoClient       chatGenerator
	vectorService    VectorService
	knowledgeService KnowledgeService
	cache            cache.Cache
	// fills 合并同一 (用户, 类型, 条数) 的并发回源。
	// 缓存刚过期时是「击穿窗口」：一批请求同时 miss，如果不合并，
	// 每个请求都会查一次库、跑一次多源生成，缓存反而放大了瞬时压力。
	fills *singleflight.Group[recommendResult]
}

type recommendResult struct {
	Items []*model.RecommendationItem `json:"items"`
	Total int64                       `json:"total"`
}

func NewRecommendService(
	recommendRepo dao.RecommendRepository,
	einoClient *eino.EinoManager,
	vectorService VectorService,
	knowledgeService KnowledgeService,
) RecommendService {
	return &recommendServiceImpl{
		recommendRepo: recommendRepo,
		// 不能直接把 *eino.EinoManager 赋给接口：初始化失败时它是 nil 指针，
		// 装进接口后会变成「非 nil 接口」，随后的 nil 判断全部失效。
		einoClient:       wrapChatGenerator(einoClient),
		vectorService:    vectorService,
		knowledgeService: knowledgeService,
		cache:            cache.NewRedisCache(),
		// 回源上限 10s：推荐生成会调向量检索/知识库，但不应无限期挂住。
		fills: singleflight.NewGroup[recommendResult](10 * time.Second),
	}
}

func wrapChatGenerator(client *eino.EinoManager) chatGenerator {
	if client == nil {
		return nil
	}
	return client
}

func (s *recommendServiceImpl) GetRecommendations(ctx context.Context, userID int64, recommendType string, limit int, reqContext map[string]string) ([]*model.RecommendationItem, int64, error) {
	logger.Info("获取推荐请求",
		logger.Int64Field("user_id", userID),
		logger.StringField("type", recommendType),
		logger.IntField("limit", limit))

	if limit < 1 || limit > 100 {
		limit = 10
	}

	cacheKey := recommendCacheKey(userID, recommendType, limit)
	if cached, ok := s.readCachedRecommendations(ctx, cacheKey); ok {
		logger.Info("从Redis缓存中获取推荐数据成功",
			logger.Int64Field("user_id", userID))
		return cached.Items, cached.Total, nil
	}

	// 缓存未命中：合并并发回源。注意回源函数里要再查一次缓存——
	// 合并窗口内可能已有别的请求把结果写回，直接查库等于白白多打一次。
	result, shared, err := s.fills.Do(ctx, cacheKey, func(fill context.Context) (recommendResult, error) {
		return s.loadRecommendations(fill, userID, recommendType, limit, cacheKey)
	})
	if err != nil {
		obs.RecordCacheFill(ctx, "recommend", "error")
		logger.Error("获取推荐回源失败", logger.ErrorField(err))
		return nil, 0, err
	}
	if shared {
		obs.RecordCacheFill(ctx, "recommend", "shared")
	} else {
		obs.RecordCacheFill(ctx, "recommend", "fill")
	}
	return result.Items, result.Total, nil
}

func (s *recommendServiceImpl) loadRecommendations(ctx context.Context, userID int64, recommendType string, limit int, cacheKey string) (recommendResult, error) {
	if cached, ok := s.readCachedRecommendations(ctx, cacheKey); ok {
		return cached, nil
	}

	items, total, err := s.recommendRepo.GetRecommendations(ctx, userID, recommendType, limit)
	if err != nil {
		return recommendResult{}, err
	}
	if len(items) > 0 {
		s.cacheRecommendations(ctx, userID, recommendType, limit, items, total)
		return recommendResult{Items: items, Total: total}, nil
	}

	logger.Info("数据库没有推荐数据，基于多源检索生成推荐")
	items, degraded := s.generateSmartRecommendations(ctx, userID, recommendType, limit)
	total = int64(len(items))

	switch {
	case len(items) > 0:
		s.cacheRecommendations(ctx, userID, recommendType, limit, items, total)
	case degraded:
		// 依赖失败导致的空结果只是降级，不能当成「该用户没有推荐」写进负缓存：
		// 那样依赖恢复后仍要等 TTL 结束才能重新生成。
		logger.Warn("推荐生成依赖失败，跳过负缓存",
			logger.Int64Field("user_id", userID),
			logger.StringField("type", recommendType))
	default:
		s.cacheEmptyRecommendations(ctx, userID, recommendType, limit)
	}
	return recommendResult{Items: items, Total: total}, nil
}

// readCachedRecommendations 读取并解析推荐缓存。
//
// 读失败与内容损坏都按未命中回源——推荐允许降级，但两者必须留下痕迹：
// 静默当成 miss 时，Redis 故障只会表现为命中率下降和下游压力上升，
// 日志里没有任何线索；损坏值如果不删除，TTL 内每个请求都会重复解析失败，
// 等于把一次脏写放大成持续的回源流量。
func (s *recommendServiceImpl) readCachedRecommendations(ctx context.Context, cacheKey string) (recommendResult, bool) {
	if s.cache == nil {
		return recommendResult{}, false
	}

	cachedData, err := s.cache.Get(ctx, cacheKey)
	if err != nil {
		logger.Warn("读取推荐缓存失败，回源生成",
			logger.StringField("cache_key", cacheKey),
			logger.ErrorField(err))
		return recommendResult{}, false
	}
	if cachedData == "" {
		return recommendResult{}, false
	}

	var cached recommendResult
	if err := json.Unmarshal([]byte(cachedData), &cached); err != nil {
		logger.Warn("推荐缓存内容损坏，删除并回源",
			logger.StringField("cache_key", cacheKey),
			logger.IntField("raw_len", len(cachedData)),
			logger.ErrorField(err))
		if delErr := s.cache.Delete(ctx, cacheKey); delErr != nil {
			logger.Warn("删除损坏的推荐缓存失败",
				logger.StringField("cache_key", cacheKey),
				logger.ErrorField(delErr))
		}
		return recommendResult{}, false
	}

	return cached, true
}

func recommendCacheKey(userID int64, recommendType string, limit int) string {
	return fmt.Sprintf("recommend:%d:%s:limit:%d", userID, recommendType, limit)
}

// generateSmartRecommendations 返回推荐列表，以及是否有检索依赖失败。
// 第二个返回值决定空结果能不能写负缓存：依赖失败时「空」是暂时的降级结果，
// 不是「这个用户确实没有推荐」。
func (s *recommendServiceImpl) generateSmartRecommendations(ctx context.Context, userID int64, recommendType string, limit int) ([]*model.RecommendationItem, bool) {
	var items []*model.RecommendationItem
	degraded := false
	now := time.Now().Unix()
	seen := make(map[string]bool)

	if s.vectorService != nil {
		query := recommendType
		if query == "" {
			query = "knowledge"
		}
		similarIDs, err := s.vectorService.SearchSimilar(ctx, query, limit)
		if err != nil {
			logger.Warn("向量搜索生成推荐失败", logger.ErrorField(err))
			degraded = true
		} else {
			for i, id := range similarIDs {
				if seen[id] {
					continue
				}
				seen[id] = true
				score := 0.9 - float64(i)*0.05
				if score < 0.5 {
					score = 0.5
				}
				items = append(items, &model.RecommendationItem{
					ID:          uuid.New().String(),
					Type:        "vector",
					Title:       fmt.Sprintf("相关内容 #%d", i+1),
					Description: fmt.Sprintf("基于向量相似度推荐的内容（相似度: %.0f%%）", score*100),
					Score:       score,
					EntityID:    id,
					CreatedAt:   now,
				})
			}
		}
	}

	if s.knowledgeService != nil {
		query := recommendType
		if query == "" {
			query = "entity"
		}
		entityIDs, err := s.knowledgeService.SearchEntities(ctx, query)
		if err != nil {
			logger.Warn("知识图谱生成推荐失败", logger.ErrorField(err))
			degraded = true
		} else {
			for i, id := range entityIDs {
				if seen[id] {
					continue
				}
				seen[id] = true
				score := 0.85 - float64(i)*0.05
				if score < 0.4 {
					score = 0.4
				}
				items = append(items, &model.RecommendationItem{
					ID:          uuid.New().String(),
					Type:        "entity",
					Title:       fmt.Sprintf("知识实体 #%d", i+1),
					Description: fmt.Sprintf("基于知识图谱关联推荐的实体（相关度: %.0f%%）", score*100),
					Score:       score,
					EntityID:    id,
					CreatedAt:   now,
				})
			}
		}
	}

	if s.einoClient != nil && len(items) > 0 {
		s.enrichWithEino(ctx, items)
	}

	if len(items) > limit {
		items = items[:limit]
	}

	return items, degraded
}

// recommendEnrichConcurrency 限制标题改写的并发度。
//
// 串行改写时 10 条推荐就是 10 次 LLM 往返顺序叠加，一次刷新就能把
// 推荐接口的 P99 推到秒级；完全放开又会撞供应商限流。4 路是折中值：
// 延迟约降为 1/4，同时在途请求数有明确上限。
const recommendEnrichConcurrency = 4

func (s *recommendServiceImpl) enrichWithEino(ctx context.Context, items []*model.RecommendationItem) {
	sem := make(chan struct{}, recommendEnrichConcurrency)
	var wg sync.WaitGroup
	for _, item := range items {
		if item == nil || !needsGeneratedTitle(item) {
			continue
		}
		if ctx.Err() != nil {
			// 调用方已取消：不再排队打 LLM，已发出的调用会在各自 ctx 上返回。
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(item *model.RecommendationItem) {
			defer wg.Done()
			defer func() { <-sem }()

			prompt := fmt.Sprintf("请为以下推荐内容生成一个简洁的中文标题（不超过20字），不要加引号：\n描述：%s\n实体ID：%s", item.Description, item.EntityID)
			result, err := s.einoClient.Chat(ctx, []string{prompt})
			if err != nil {
				logger.Warn("Eino生成标题失败", logger.ErrorField(err))
				return
			}
			if result != "" {
				item.Title = result
			}
		}(item)
	}
	wg.Wait()
}

// needsGeneratedTitle 判断标题是否只是占位文案。
// 用户/数据库给出的真实标题不能被模型覆盖。
func needsGeneratedTitle(item *model.RecommendationItem) bool {
	return item.Title == "" || startsWith(item.Title, "相关内容") || startsWith(item.Title, "知识实体")
}

func startsWith(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

func (s *recommendServiceImpl) cacheRecommendations(ctx context.Context, userID int64, recommendType string, limit int, items []*model.RecommendationItem, total int64) {
	if s.cache == nil {
		return
	}

	// 写回必须用「请求的 limit」而不是 len(items)：否则数据库只返回 3 条时
	// 会写到 limit:3 这个 key，下一次 limit:10 的请求永远命中不了，
	// 缓存命中率被人为归零。
	cacheKey := recommendCacheKey(userID, recommendType, limit)
	dataJSON, err := json.Marshal(recommendResult{Items: items, Total: total})
	if err != nil {
		logger.Error("序列化推荐数据失败，跳过写缓存",
			logger.Int64Field("user_id", userID),
			logger.ErrorField(err))
		return
	}

	err = s.cache.Set(ctx, cacheKey, string(dataJSON), recommendCacheTTL)
	if err != nil {
		logger.Warn("缓存推荐数据到Redis失败",
			logger.Int64Field("user_id", userID),
			logger.ErrorField(err))
	} else {
		logger.Info("已缓存推荐数据到Redis",
			logger.Int64Field("user_id", userID),
			logger.StringField("cache_key", cacheKey),
			logger.IntField("item_count", len(items)))
	}
}

// cacheEmptyRecommendations 为空结果写一个短 TTL 负缓存。
//
// 没有这层缓存时，一个没有推荐数据的用户每次刷新都会完整重跑
// 「查库 → 向量检索 → 知识图谱检索 → 逐条 LLM 改写」，
// 空结果反而成了最贵的请求，还能被轮询放大成对下游的持续压力。
// TTL 必须比正常结果短得多：新数据最多延迟一分钟可见。
func (s *recommendServiceImpl) cacheEmptyRecommendations(ctx context.Context, userID int64, recommendType string, limit int) {
	if s.cache == nil {
		return
	}
	cacheKey := recommendCacheKey(userID, recommendType, limit)
	dataJSON, err := json.Marshal(recommendResult{})
	if err != nil {
		logger.Error("序列化空推荐结果失败，跳过负缓存",
			logger.Int64Field("user_id", userID),
			logger.ErrorField(err))
		return
	}
	if err := s.cache.Set(ctx, cacheKey, string(dataJSON), emptyRecommendCacheTTL); err != nil {
		logger.Warn("缓存空推荐结果失败",
			logger.Int64Field("user_id", userID),
			logger.ErrorField(err))
		return
	}
	logger.Info("已缓存空推荐结果",
		logger.Int64Field("user_id", userID),
		logger.StringField("cache_key", cacheKey))
}

const (
	recommendCacheTTL = 30 * time.Minute
	// 负缓存只挡住「短时间内的重复刷新」，不能让新数据长时间不可见。
	emptyRecommendCacheTTL = 60 * time.Second
)

func (s *recommendServiceImpl) GetRelatedRecommendations(ctx context.Context, entityID string, recommendType string, limit int) ([]*model.RecommendationItem, int64, error) {
	logger.Info("获取相关推荐请求",
		logger.StringField("entity_id", entityID),
		logger.StringField("type", recommendType),
		logger.IntField("limit", limit))

	if entityID == "" {
		return nil, 0, errors.New("实体ID不能为空")
	}

	if limit < 1 || limit > 100 {
		limit = 10
	}

	// 向量检索和知识图谱检索互不依赖，串行执行只是把两段网络延迟相加。
	// 两边都允许各自失败降级（失败等于空结果），所以不用 errgroup 的
	// 「一个失败就取消另一个」语义，各自记录日志即可。
	var (
		vectorIDs    []string
		knowledgeIDs []string
		wg           sync.WaitGroup
	)
	if s.vectorService != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			similarResults, err := s.vectorService.SearchSimilar(ctx, entityID, limit)
			if err != nil {
				logger.Warn("向量搜索失败", logger.ErrorField(err))
				return
			}
			vectorIDs = similarResults
			logger.Info("从向量搜索获取相关推荐",
				logger.IntField("count", len(similarResults)))
		}()
	}
	if s.knowledgeService != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			entities, err := s.knowledgeService.SearchEntities(ctx, entityID)
			if err != nil {
				logger.Warn("知识图谱搜索失败", logger.ErrorField(err))
				return
			}
			knowledgeIDs = entities
			logger.Info("从知识图谱获取相关实体",
				logger.IntField("count", len(entities)))
		}()
	}
	wg.Wait()

	items, total, err := s.recommendRepo.GetRelatedRecommendations(ctx, entityID, recommendType, limit)
	if err != nil {
		logger.Error("从数据库获取相关推荐失败", logger.ErrorField(err))
		return nil, 0, err
	}

	now := time.Now().Unix()
	seen := make(map[string]bool)
	for _, item := range items {
		if item.EntityID != "" {
			seen[item.EntityID] = true
		}
	}

	for i, id := range vectorIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		score := 0.9 - float64(i)*0.05
		if score < 0.5 {
			score = 0.5
		}
		items = append(items, &model.RecommendationItem{
			ID:          uuid.New().String(),
			Type:        "vector",
			Title:       fmt.Sprintf("向量相似内容 #%d", i+1),
			Description: fmt.Sprintf("基于向量相似度推荐（相似度: %.0f%%）", score*100),
			Score:       score,
			EntityID:    id,
			CreatedAt:   now,
		})
	}

	for i, id := range knowledgeIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		score := 0.85 - float64(i)*0.05
		if score < 0.4 {
			score = 0.4
		}
		items = append(items, &model.RecommendationItem{
			ID:          uuid.New().String(),
			Type:        "entity",
			Title:       fmt.Sprintf("知识图谱关联 #%d", i+1),
			Description: fmt.Sprintf("基于知识图谱关联推荐（相关度: %.0f%%）", score*100),
			Score:       score,
			EntityID:    id,
			CreatedAt:   now,
		})
	}

	if s.einoClient != nil && len(items) > 0 {
		s.enrichWithEino(ctx, items)
	}

	total = int64(len(items))
	if len(items) > limit {
		items = items[:limit]
		total = int64(len(items))
	}

	return items, total, nil
}

func (s *recommendServiceImpl) SubmitFeedback(ctx context.Context, itemID string, userID int64, action string, timestamp int64) error {
	logger.Info("提交推荐反馈请求",
		logger.StringField("item_id", itemID),
		logger.Int64Field("user_id", userID),
		logger.StringField("action", action))

	if itemID == "" {
		return errors.New("推荐项ID不能为空")
	}
	if action == "" {
		return errors.New("操作类型不能为空")
	}

	if timestamp == 0 {
		timestamp = time.Now().Unix()
	}

	itemType := "unknown"
	title := itemID
	item, err := s.recommendRepo.GetRecommendationByID(ctx, itemID)
	switch {
	case err != nil:
		// 反馈本身是用户行为数据，不能因为补全元信息失败就丢掉；
		// 但必须留下日志，否则 item_type 长期堆积 unknown 也无人察觉。
		logger.Warn("查询推荐项失败，反馈以降级元信息落库",
			logger.StringField("item_id", itemID),
			logger.Int64Field("user_id", userID),
			logger.ErrorField(err))
	case item == nil:
		logger.Warn("反馈对应的推荐项不存在，反馈以降级元信息落库",
			logger.StringField("item_id", itemID),
			logger.Int64Field("user_id", userID))
	default:
		if item.Type != "" {
			itemType = item.Type
		}
		if item.Title != "" {
			title = item.Title
		}
	}

	history := &model.RecommendationHistory{
		ID:        uuid.New().String(),
		ItemID:    itemID,
		ItemType:  itemType,
		Title:     title,
		Action:    action,
		UserID:    userID,
		Timestamp: timestamp,
	}

	err = s.recommendRepo.SaveHistory(ctx, history)
	if err != nil {
		logger.Error("保存推荐历史失败", logger.ErrorField(err))
		return err
	}

	return nil
}

func (s *recommendServiceImpl) GetRecommendationHistory(ctx context.Context, userID int64, page, pageSize int) ([]*model.RecommendationHistory, int64, error) {
	logger.Info("获取推荐历史请求",
		logger.Int64Field("user_id", userID),
		logger.IntField("page", page),
		logger.IntField("page_size", pageSize))

	page, pageSize = pagination.Normalize(page, pageSize)

	histories, total, err := s.recommendRepo.GetHistory(ctx, userID, page, pageSize)
	if err != nil {
		logger.Error("获取推荐历史失败", logger.ErrorField(err))
		return nil, 0, err
	}

	return histories, total, nil
}

func (s *recommendServiceImpl) BatchGetRecommendations(ctx context.Context, userIDs []int64, recommendType string, limit int) (map[int64][]*model.RecommendationItem, error) {
	logger.Info("批量获取推荐请求",
		logger.IntField("user_count", len(userIDs)),
		logger.StringField("type", recommendType),
		logger.IntField("limit", limit))

	if len(userIDs) == 0 {
		return nil, errors.New("用户ID列表不能为空")
	}

	if limit < 1 || limit > 100 {
		limit = 10
	}

	return s.batchRecommendations(ctx, userIDs, recommendType, limit), nil
}

// recommendationBatchConcurrency 限制批量回源的并发度。
//
// 为什么不做成「每个用户一个 goroutine」：100 个用户就是 100 个并发查询，
// 直接打满连接池，反而把其他接口的延迟一起拖高。串行的代价则是 N 次
// 往返叠加；这里取中间值——固定 8 路，既有并发收益又有明确上限。
const recommendationBatchConcurrency = 8

func (s *recommendServiceImpl) batchRecommendations(ctx context.Context, userIDs []int64, recommendType string, limit int) map[int64][]*model.RecommendationItem {
	result := make(map[int64][]*model.RecommendationItem, len(userIDs))
	var mu sync.Mutex
	sem := make(chan struct{}, recommendationBatchConcurrency)
	var wg sync.WaitGroup

	for _, userID := range userIDs {
		wg.Add(1)
		sem <- struct{}{}
		go func(userID int64) {
			defer wg.Done()
			defer func() { <-sem }()

			items, _, err := s.GetRecommendations(ctx, userID, recommendType, limit, nil)

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				logger.Warn("获取用户推荐失败",
					logger.Int64Field("user_id", userID),
					logger.ErrorField(err))
				result[userID] = []*model.RecommendationItem{}
				return
			}
			result[userID] = items
		}(userID)
	}
	wg.Wait()
	return result
}
