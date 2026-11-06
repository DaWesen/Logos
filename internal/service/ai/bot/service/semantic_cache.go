package service

import (
	"context"
	"errors"
	"time"

	"Logos/config"
	botmodel "Logos/internal/service/ai/bot/model"
	"Logos/internal/service/ai/bot/service/scache"
	"Logos/pkg/cache"
	"Logos/pkg/eino"
	"Logos/pkg/obs"
)

// EmbedForBotFunc 返回指定 Bot 语境下文本的向量。
// Bot 配置了自定义 embedding 模型时优先使用，否则回退全局模型。
type EmbedForBotFunc func(ctx context.Context, bot *botmodel.Bot, text string) ([]float32, error)

// SemanticCache 是 scache v2 的服务层门面。
//
// 这一层只做三件事：从 Bot/用户构造 Scope、把 Bot 级灰度映射成 Mode、
// 把 embedding 适配成 scache.EmbedFunc。判定、索引、持久化和降级全部
// 留在 scache 包内，避免业务代码再次出现一套「差不多」的缓存逻辑。
type SemanticCache struct {
	core  *scache.Cache
	embed EmbedForBotFunc
}

// NewSemanticCache 保留 v1 构造函数签名，供已有装配代码平滑迁移。
// addr/password/db 仅为兼容旧调用方；真正的 Redis 句柄由 pkg/cache 单例提供。
func NewSemanticCache(addr, password string, db int, embed EmbedForBotFunc) *SemanticCache {
	return NewSemanticCacheWithJudge(addr, password, db, embed, nil)
}

// NewSemanticCacheWithJudge 装配判官。judge 为 nil 时灰区一律按 miss，
// 缓存仍可在高阈值区间工作。
func NewSemanticCacheWithJudge(addr, password string, db int, embed EmbedForBotFunc, judge scache.JudgeFunc) *SemanticCache {
	_ = addr
	_ = password
	_ = db

	cfg, err := semanticCacheConfig(judge)
	if err != nil {
		// 配置非法时让缓存整体停机，而不是带着错误阈值运行。
		// 服务本身仍能启动，因为缓存是可选优化。
		return &SemanticCache{embed: embed}
	}
	core, err := scache.New(cfg)
	if err != nil {
		return &SemanticCache{embed: embed}
	}
	return &SemanticCache{core: core, embed: embed}
}

func semanticCacheConfig(judge scache.JudgeFunc) (scache.Config, error) {
	cfg := scache.DefaultConfig()
	cfg.CacheProvider = func() cache.Cache {
		if config.TryGetConfig() == nil {
			return nil
		}
		return cache.NewRedisCache()
	}
	cfg.Judge = judge

	app := config.TryGetConfig()
	if app == nil {
		if judge == nil {
			cfg.JudgeEnabled = false
		}
		return cfg, nil
	}
	sc := app.SemanticCache

	if sc.Mode != "" {
		mode, err := scache.ParseMode(sc.Mode)
		if err != nil {
			return cfg, err
		}
		cfg.Mode = mode
	}
	if sc.MidThreshold > 0 {
		cfg.MidThreshold = sc.MidThreshold
	}
	if sc.HighThreshold > 0 {
		cfg.HighThreshold = sc.HighThreshold
	}
	if sc.MaxEntriesPerScope > 0 {
		cfg.MaxEntriesPerScope = sc.MaxEntriesPerScope
	}
	if sc.MaxTotalEntries > 0 {
		cfg.MaxTotalEntries = sc.MaxTotalEntries
	}
	if sc.MaxTotalBytes > 0 {
		cfg.MaxTotalBytes = sc.MaxTotalBytes
	}
	if sc.EntryTTLHours > 0 {
		cfg.EntryTTL = time.Duration(sc.EntryTTLHours) * time.Hour
	}
	if sc.ExactTTLHours > 0 {
		cfg.ExactTTL = time.Duration(sc.ExactTTLHours) * time.Hour
	}
	cfg.JudgeEnabled = sc.JudgeEnabled
	if judge == nil {
		cfg.JudgeEnabled = false
	}
	if sc.JudgeTimeoutMS > 0 {
		cfg.JudgeTimeout = time.Duration(sc.JudgeTimeoutMS) * time.Millisecond
	}
	if sc.WriteQueueSize > 0 {
		cfg.WriteQueueSize = sc.WriteQueueSize
	}
	if sc.WriteWorkers > 0 {
		cfg.WriteWorkers = sc.WriteWorkers
	}
	if sc.ReplayChunkRunes > 0 {
		cfg.ReplayChunkRunes = sc.ReplayChunkRunes
	}
	if sc.ReplayBudgetMS > 0 {
		cfg.ReplayBudget = time.Duration(sc.ReplayBudgetMS) * time.Millisecond
	}
	cfg.CalibratedFor = sc.CalibratedFor
	return cfg, nil
}

// Lookup 查询语义缓存。任何失败都静默降级为未命中。
func (c *SemanticCache) Lookup(ctx context.Context, bot *botmodel.Bot, userID, query string) (string, bool) {
	if c == nil || c.core == nil || c.embed == nil {
		obs.RecordSemanticCache(ctx, "lookup", "disabled")
		return "", false
	}

	result := c.core.Lookup(ctx, scache.LookupRequest{
		Scope: c.scopeFor(ctx, bot, userID),
		Query: query,
		Embed: c.embedFunc(bot),
		Mode:  scacheModeForBot(bot),
	})
	if result.Decision.Hit() {
		obs.RecordSemanticCache(ctx, "lookup", "hit")
		return result.Answer, true
	}
	obs.RecordSemanticCache(ctx, "lookup", "miss")
	return "", false
}

// Store 写入语义缓存（进入有界队列，不阻塞主链路）。
func (c *SemanticCache) Store(ctx context.Context, bot *botmodel.Bot, userID, query, answer string) {
	c.StoreWithMeta(ctx, bot, userID, query, answer, "chat", false, false)
}

// StoreWithMeta 携带准入判断所需的元信息。
// source/toolCalled/injectionGuardHit 任一触发禁区，答案都会被拒绝入库。
func (c *SemanticCache) StoreWithMeta(ctx context.Context, bot *botmodel.Bot, userID, query, answer,
	source string, toolCalled, injectionGuardHit bool) {
	if c == nil || c.core == nil || c.embed == nil {
		return
	}
	c.core.Store(ctx, scache.StoreRequest{
		Scope:             c.scopeFor(ctx, bot, userID),
		Query:             query,
		Answer:            answer,
		Embed:             c.embedFunc(bot),
		Source:            source,
		ToolCalled:        toolCalled,
		InjectionGuardHit: injectionGuardHit,
		Mode:              scacheModeForBot(bot),
	})
}

// ReplayStream 把缓存答案按伪流式节奏回放。
func (c *SemanticCache) ReplayStream(answer string, onChunk func(string) error) error {
	if c == nil || c.core == nil {
		return onChunk(answer)
	}
	return c.core.ReplayStream(answer, onChunk)
}

// BumpKBVersion 在知识库写入成功后推进版本，使旧答案立即不可达。
func (c *SemanticCache) BumpKBVersion(ctx context.Context, botID string) int64 {
	if c == nil || c.core == nil {
		return 0
	}
	return c.core.BumpKBVersion(ctx, botID)
}

// Stats 返回当前进程内索引规模，供监控与测试使用。
func (c *SemanticCache) Stats() (entries int, bytes int64) {
	if c == nil || c.core == nil {
		return 0, 0
	}
	return c.core.Stats()
}

// Close 停止后台 worker。重复调用安全。
func (c *SemanticCache) Close() {
	if c == nil || c.core == nil {
		return
	}
	c.core.Close()
}

func (c *SemanticCache) scopeFor(ctx context.Context, bot *botmodel.Bot, userID string) scache.Scope {
	botID := "default"
	prompt, modelKey, embedKey := "", "", "global"
	if bot != nil {
		if bot.ID != "" {
			botID = bot.ID
		}
		prompt = bot.SystemPrompt
		modelKey = bot.Provider + "/" + bot.Model + "/t=0"
		if bot.EmbeddingModel != "" {
			embedKey = bot.EmbeddingModel
		}
	}
	return scache.Scope{
		Tenant:     "default",
		BotID:      botID,
		UserID:     userID,
		KBVersion:  c.core.KBVersion(ctx, botID),
		PromptHash: scache.PromptHash(prompt),
		ModelKey:   modelKey,
		EmbedKey:   embedKey,
	}
}

func (c *SemanticCache) embedFunc(bot *botmodel.Bot) scache.EmbedFunc {
	return func(ctx context.Context, text string) ([]float32, error) {
		return c.embed(ctx, bot, text)
	}
}

// scacheModeForBot 把 Bot 级配置映射为灰度模式。
// 非法值按 off 处理：拼错开关名时宁可不用缓存，也不要猜调用方意图。
func scacheModeForBot(bot *botmodel.Bot) scache.Mode {
	if bot == nil {
		return ""
	}
	if bot.Config["enable_semantic_cache"] == "false" {
		return scache.ModeOff
	}
	if raw := bot.Config["scache_mode"]; raw != "" {
		mode, err := scache.ParseMode(raw)
		if err != nil {
			return scache.ModeOff
		}
		return mode
	}
	return ""
}

// cosineSimilarity 保留为纯函数，供既有测试与外部调试使用。
func cosineSimilarity(a, b []float32) float64 {
	return scache.CosineSimilarity(a, b)
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

// semanticCacheJudge 把灰区判官接到当前服务的对话模型。
//
// 提示词由 scache 包维护，装配层只负责发起一次普通对话调用。判官
// 失败、超时或输出不可解析都会被缓存层降级为 miss，不影响主链路。
func (s *botServiceImpl) semanticCacheJudge(ctx context.Context, prompt string) (string, error) {
	if s.einoManager == nil || !s.einoManager.HasChatModel() {
		return "", errors.New("无可用的判官模型")
	}
	return s.einoManager.Chat(ctx, []string{scache.JudgeSystemPrompt(), prompt})
}

// storeSemanticCache 后台写入语义缓存（独立 context，不受请求取消影响）。
func (s *botServiceImpl) storeSemanticCache(bot *botmodel.Bot, userID, query, answer string) {
	s.storeSemanticCacheWithMeta(bot, userID, query, answer, "chat", false, false)
}

func (s *botServiceImpl) storeSemanticCacheWithMeta(bot *botmodel.Bot, userID, query, answer,
	source string, toolCalled, injectionGuardHit bool) {
	if s.semanticCache == nil {
		return
	}
	go func() {
		storeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		s.semanticCache.StoreWithMeta(storeCtx, bot, userID, query, answer, source, toolCalled, injectionGuardHit)
	}()
}

func toFloat32(v []float64) []float32 {
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = float32(x)
	}
	return out
}
