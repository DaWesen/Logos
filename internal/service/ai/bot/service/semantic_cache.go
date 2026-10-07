package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	botmodel "Logos/internal/service/ai/bot/model"
	"Logos/pkg/eino"
	"Logos/pkg/logger"

	"github.com/redis/go-redis/v9"
)

const (
	semanticCacheKeyPrefix  = "logos:scache"
	semanticCacheMaxEntries = 50                 // 每个 (用户, Bot) 保留的缓存条数
	semanticCacheTTL        = 24 * time.Hour     // Redis key 整体过期
	semanticCacheEntryTTL   = 12 * time.Hour     // 单条目有效期（查询时判断）
	semanticCacheThreshold  = 0.90               // 余弦相似度命中阈值
	semanticCacheDialSkip   = 24 * time.Hour     // 拨号失败后多久内不再重试
)

// SemanticCache 语义缓存：对「语义相似」的重复问题直接返回缓存答案，
// 省去一次 LLM 调用（省钱 + 毫秒级响应）。
//
// 设计要点：
//   - 查询向量化与历史条目做余弦相似度，超过阈值即命中；
//   - 向量必须来自同一 embedding 模型（按 Bot 配置解析），否则相似度无意义；
//   - Redis 惰性拨号，失败后在冷却期内自动降级为直通（不影响主流程）；
//   - Store 在后台 goroutine 中调用，不阻塞对话主链路。
type SemanticCache struct {
	mu       sync.Mutex
	client   *redis.Client
	lastFail time.Time // 上次拨号失败时间，冷却期内跳过
	cfg      redisOptions
	embed    EmbedForBotFunc
}

type redisOptions struct {
	Addr     string
	Password string
	DB       int
}

// EmbedForBotFunc 返回指定 Bot 语境下文本的向量。
// Bot 配置了自定义 embedding 模型时优先使用，否则回退全局模型。
type EmbedForBotFunc func(ctx context.Context, bot *botmodel.Bot, text string) ([]float32, error)

type semanticEntry struct {
	Query     string    `json:"query"`
	Answer    string    `json:"answer"`
	Vector    []float32 `json:"vector"`
	CreatedAt int64     `json:"created_at"`
}

func NewSemanticCache(addr, password string, db int, embed EmbedForBotFunc) *SemanticCache {
	return &SemanticCache{
		cfg: redisOptions{Addr: addr, Password: password, DB: db},
		embed: embed,
	}
}

func semanticCacheKey(userID, botID string) string {
	return fmt.Sprintf("%s:%s:%s", semanticCacheKeyPrefix, userID, botID)
}

// getClient 惰性建立 Redis 连接；拨号失败进入冷却期，期间缓存整体直通
func (c *SemanticCache) getClient(ctx context.Context) (*redis.Client, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.client != nil {
		return c.client, nil
	}
	if time.Since(c.lastFail) < semanticCacheDialSkip {
		return nil, errors.New("semantic cache redis disabled in cooldown")
	}

	client := redis.NewClient(&redis.Options{
		Addr:     c.cfg.Addr,
		Password: c.cfg.Password,
		DB:       c.cfg.DB,
		PoolSize: 4,
	})
	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		_ = client.Close()
		c.lastFail = time.Now()
		return nil, fmt.Errorf("semantic cache redis dial failed: %w", err)
	}

	c.client = client
	logger.Info("语义缓存 Redis 已连接", logger.StringField("addr", c.cfg.Addr))
	return client, nil
}

// Lookup 查询语义缓存。任何失败都静默降级为未命中，保证不影响主流程。
func (c *SemanticCache) Lookup(ctx context.Context, bot *botmodel.Bot, userID, query string) (string, bool) {
	if c == nil || c.embed == nil || query == "" {
		return "", false
	}

	vec, err := c.embed(ctx, bot, query)
	if err != nil || len(vec) == 0 {
		return "", false
	}

	client, err := c.getClient(ctx)
	if err != nil {
		return "", false
	}

	// 向量化是一次 API 调用（几十毫秒），Redis 读取同样设置短超时
	cacheCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	raw, err := client.LRange(cacheCtx, semanticCacheKey(userID, botID(bot)), 0, semanticCacheMaxEntries-1).Result()
	if err != nil {
		return "", false
	}

	now := time.Now().Unix()
	var best string
	bestSim := 0.0
	for _, item := range raw {
		var entry semanticEntry
		if json.Unmarshal([]byte(item), &entry) != nil {
			continue
		}
		if now-entry.CreatedAt > int64(semanticCacheEntryTTL/time.Second) {
			continue
		}
		if sim := cosineSimilarity(vec, entry.Vector); sim > bestSim {
			bestSim = sim
			best = entry.Answer
		}
	}

	if bestSim >= semanticCacheThreshold {
		logger.Info("语义缓存命中",
			logger.StringField("user_id", userID),
			logger.Float64Field("similarity", bestSim))
		return best, true
	}
	return "", false
}

// Store 写入语义缓存（应在后台 goroutine 中调用）。
// Lua 保证 LPUSH + LTRIM + EXPIRE 原子执行。
var semanticCacheStoreScript = redis.NewScript(`
redis.call('LPUSH', KEYS[1], ARGV[1])
redis.call('LTRIM', KEYS[1], 0, tonumber(ARGV[2]))
redis.call('EXPIRE', KEYS[1], tonumber(ARGV[3]))
`)

func (c *SemanticCache) Store(ctx context.Context, bot *botmodel.Bot, userID, query, answer string) {
	if c == nil || c.embed == nil || query == "" || answer == "" {
		return
	}

	vec, err := c.embed(ctx, bot, query)
	if err != nil || len(vec) == 0 {
		return
	}

	client, err := c.getClient(ctx)
	if err != nil {
		return
	}

	entry := semanticEntry{
		Query:     query,
		Answer:    answer,
		Vector:    vec,
		CreatedAt: time.Now().Unix(),
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}

	storeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	_ = semanticCacheStoreScript.Run(storeCtx, client,
		[]string{semanticCacheKey(userID, botID(bot))},
		string(data),
		semanticCacheMaxEntries-1,
		int(semanticCacheTTL/time.Second),
	).Err()
}

// botID 从 Bot 对象提取缓存命名空间使用的 ID
func botID(bot *botmodel.Bot) string {
	if bot == nil {
		return "default"
	}
	return bot.ID
}

// cosineSimilarity 计算两个向量的余弦相似度（纯函数，可单测）
func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

// BuildEmbedForBot 构造按 Bot 配置解析 embedding 的函数：
// Bot 自定义 embedding 模型优先，回退全局 EinoManager。
func (s *botServiceImpl) embedForBot(ctx context.Context, bot *botmodel.Bot, text string) ([]float32, error) {
	if bot != nil && bot.EmbeddingModel != "" {
		apiKey := bot.Config["embedding_api_key"]
		if apiKey == "" {
			apiKey = bot.APIKey
		}
		embedder, err := eino.NewDynamicEmbedder(apiKey, bot.EmbeddingModel, bot.Config["embedding_base_url"])
		if err == nil {
			if vecs, err := embedder.EmbedStrings(ctx, []string{text}); err == nil && len(vecs) > 0 {
				return toFloat32(vecs[0]), nil
			}
		}
	}

	if s.einoManager == nil {
		return nil, errors.New("无可用的 embedding 模型")
	}
	vec, err := s.einoManager.EmbedText(ctx, text)
	if err != nil {
		return nil, err
	}
	return toFloat32(vec), nil
}

// storeSemanticCache 后台写入语义缓存（独立 context，不受请求取消影响）
func (s *botServiceImpl) storeSemanticCache(bot *botmodel.Bot, userID, query, answer string) {
	if s.semanticCache == nil {
		return
	}
	go func() {
		storeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		s.semanticCache.Store(storeCtx, bot, userID, query, answer)
	}()
}

func toFloat32(v []float64) []float32 {
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = float32(x)
	}
	return out
}
