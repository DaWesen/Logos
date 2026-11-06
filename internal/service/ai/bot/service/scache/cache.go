package scache

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"regexp"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"Logos/pkg/cache"
	"Logos/pkg/logger"
	"Logos/pkg/obs"
	"Logos/pkg/singleflight"
)

const (
	redisKeyPrefix = "logos:scache:v2"
	kbVersionKey   = "logos:kb:ver:%s"

	redisFailureCooldown = 30 * time.Second
	redisRetryAfterFail  = 30 * time.Second
	redisColdLoadTimeout = 300 * time.Millisecond
	redisPersistTimeout  = 3 * time.Second
	kbVersionLocalTTL    = 10 * time.Second
)

var (
	errNoEmbedding = errors.New("scache: no embedding function")
	errBreakerOpen = errors.New("scache: breaker open")
	errNoRedis     = errors.New("scache: redis unavailable")

	// 兜底文案入库的代价是用户连续多次拿到同一个错误结果，且刷新无效。
	degradedAnswerPattern = regexp.MustCompile(`(?i)无法回答|抱歉[^。\n]{0,16}不能|转人工|出错了|请稍后重试`)

	// 价格/库存/活动这类答案有强时效性，按 30 分钟档缓存。
	priceSensitivePattern = regexp.MustCompile(`库存|优惠|活动|折扣|促销|售价|报价`)

	// 个人数据一旦被缓存，换设备/换会话仍可能被召回，直接拒绝写入。
	personalDataPattern = regexp.MustCompile(`我的|账号|账户|订单|余额|手机号|身份证|银行卡|收货地址|发票`)
)

// Cache 是语义缓存的核心。
//
// 它只依赖两个外部能力：embedding 与 pkg/cache 单例。Redis 故障、
// embedding 超时、判官失败都会被降级成 miss，调用方永远拿不到缓存错误
// （不变量 I2）。
type Cache struct {
	cfg  Config
	mode Mode
	idx  *index
	sig  *signals

	writeCh chan writeJob
	stopCh  chan struct{}
	wg      sync.WaitGroup
	closed  atomic.Bool

	redisMu        sync.Mutex
	redis          cache.Cache
	redisResolving bool
	redisNextTry   time.Time

	redisCB *breaker
	embedCB *breaker
	judgeCB *breaker

	// 同一 scope + 同一问题的并发 embedding 只真正调用一次。scope 含
	// UserID，因此合并不会跨用户泄漏；不同问题仍然各算各的。
	embeds *singleflight.Group[[]float32]

	// 同一 scope + 归一化问题的写入串行化。两个 worker 同时处理同一问题
	// 时，若「删旧条目」与「写新条目」交错，本地索引与 Redis 会分叉。
	writeLocks []sync.Mutex

	loadMu sync.Mutex
	loaded map[string]loadState

	verMu    sync.Mutex
	versions map[string]versionState
}

type writeJob struct {
	entry *Entry
	scope Scope
	embed EmbedFunc
	text  string
}

type loadState struct {
	loaded  bool
	loading bool
	at      time.Time
}

type versionState struct {
	version int64
	at      time.Time
}

// New 校验配置并启动写入 worker 与淘汰 ticker。
//
// 调用方应先从 DefaultConfig() 出发再覆盖字段：本函数不猜「零值是不是
// 想表达默认值」，尤其是 JudgeEnabled=false 这种合法配置。
func New(cfg Config) (*Cache, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Rand == nil {
		cfg.Rand = randFloat
	}

	c := &Cache{
		cfg:        cfg,
		mode:       cfg.Mode,
		idx:        newIndex(cfg.MaxEntriesPerScope),
		sig:        newSignals(1024, cfg.SignalsTTL),
		writeCh:    make(chan writeJob, cfg.WriteQueueSize),
		stopCh:     make(chan struct{}),
		redisCB:    newBreaker(10, redisFailureCooldown),
		embedCB:    newBreaker(5, 60*time.Second),
		judgeCB:    newBreaker(5, 5*time.Minute),
		embeds:     singleflight.NewGroup[[]float32](cfg.EmbedTimeout),
		writeLocks: make([]sync.Mutex, 64),
		loaded:     make(map[string]loadState),
		versions:   make(map[string]versionState),
	}

	for i := 0; i < cfg.WriteWorkers; i++ {
		c.wg.Add(1)
		go c.writeLoop()
	}
	c.wg.Add(1)
	go c.evictLoop()
	return c, nil
}

// Lookup 执行完整判定流水线。
//
// 顺序不可调换：开关 → 归一化 → 精确命中 → 槽位门控 → embedding →
// 向量检索 → 槽位兼容 → 判官。便宜的判断永远在贵的调用之前。
func (c *Cache) Lookup(ctx context.Context, req LookupRequest) LookupResult {
	if c == nil {
		obs.RecordSCacheLookup(ctx, string(MissDisabled))
		return LookupResult{Decision: MissDisabled}
	}
	mode := c.effectiveMode(req.Mode)
	if mode == ModeOff || req.Query == "" {
		return c.miss(ctx, LookupResult{}, MissDisabled)
	}
	if err := req.Scope.Validate(); err != nil {
		return c.miss(ctx, LookupResult{}, MissDisabled)
	}

	nq := Normalize(req.Query)
	if nq == "" {
		return c.miss(ctx, LookupResult{}, MissNoSlots)
	}
	scopeHash := req.Scope.Hash()
	now := c.cfg.Now()
	result := LookupResult{ScopeHash: scopeHash}

	if c.sig.rejected(scopeHash, nq, now) {
		obs.RecordSCacheFalseHit(ctx, "reject")
	}

	exactStart := now
	if entry := c.exactLookup(ctx, scopeHash, nq, now); entry != nil {
		obs.RecordSCacheStage(ctx, "exact", time.Since(exactStart))
		return c.finishHit(ctx, result, HitExact, entry, 1, mode, now)
	}
	obs.RecordSCacheStage(ctx, "exact", time.Since(exactStart))

	slotStart := c.cfg.Now()
	slots := Extract(req.Query)
	anaphoric := slots.Empty() && TooShortOrAnaphoric(nq)
	obs.RecordSCacheStage(ctx, "slot", time.Since(slotStart))
	if anaphoric {
		return c.miss(ctx, result, MissNoSlots)
	}

	embedStart := c.cfg.Now()
	vec, err := c.embed(ctx, scopeHash, req.Query, req.Vector, req.Embed)
	obs.RecordSCacheStage(ctx, "embed", time.Since(embedStart))
	if err != nil || len(vec) == 0 {
		return c.miss(ctx, result, MissError)
	}
	result.Vector = vec

	if c.sig.reasked(scopeHash, vec, c.cfg.MidThreshold, now) {
		obs.RecordSCacheFalseHit(ctx, "reask")
	}

	c.ensureLoaded(ctx, scopeHash, now)

	searchStart := c.cfg.Now()
	cands := c.idx.search(scopeHash, vec, c.cfg.TopK, now.Unix())
	obs.RecordSCacheStage(ctx, "search", time.Since(searchStart))
	result.Candidates = len(cands)
	if len(cands) == 0 {
		return c.miss(ctx, result, MissLowSim)
	}

	// 先按槽位过滤，再在兼容候选里取相似度最高的一条。
	// 不能只看 cands[0]：最相似的那条可能槽位冲突，而第二条完全可用。
	compatible := make([]Candidate, 0, len(cands))
	for _, cand := range cands {
		if slots.Compatible(cand.Entry.Slots) {
			compatible = append(compatible, cand)
		}
	}
	if len(compatible) == 0 {
		return c.miss(ctx, result, MissSlot)
	}
	best := compatible[0]
	result.Similarity = best.Similarity
	result.EntryID = best.Entry.ID

	switch {
	case best.Similarity >= c.cfg.HighThreshold:
		return c.finishHit(ctx, result, HitVector, best.Entry, best.Similarity, mode, now)
	case best.Similarity >= c.cfg.MidThreshold:
		judgeStart := c.cfg.Now()
		// 只把最终会返回的那一条交给判官。若把 top-k 全给它，判官可能
		// 因为候选 2 判 SAME，而代码仍返回候选 1——批准对象与返回对象
		// 必须严格一致，否则灰区判定会引入一类无法复现的假命中。
		verdict := c.judge(ctx, req.Query, compatible[:1])
		obs.RecordSCacheStage(ctx, "judge", time.Since(judgeStart))
		obs.RecordSCacheJudge(ctx, string(verdict))
		if verdict == JudgeSame {
			return c.finishHit(ctx, result, HitJudge, best.Entry, best.Similarity, mode, now)
		}
		return c.miss(ctx, result, MissJudge)
	default:
		return c.miss(ctx, result, MissLowSim)
	}
}

func (c *Cache) finishHit(ctx context.Context, result LookupResult, decision Decision,
	entry *Entry, similarity float64, mode Mode, now time.Time) LookupResult {

	result.Decision = decision
	result.Answer = entry.Answer
	result.EntryID = entry.ID
	result.Similarity = similarity
	result.EntryAge = now.Sub(time.Unix(entry.CreatedAt, 0))

	c.idx.touch(entry.ScopeHash, entry.ID)
	c.sig.remember(entry.ScopeHash, entry.NormQuery, entry.Vec, now)

	// 一次 Lookup 只能记一次结论。shadow 模式下对外结论是 miss_shadowed，
	// 不能再额外记一条 hit_*，否则命中率分母会被同一次请求重复计数。
	effective := decision
	if mode == ModeShadow {
		effective = MissShadowed
	}
	obs.RecordSCacheLookup(ctx, string(effective))
	obs.RecordSCacheSimilarity(ctx, string(decision), similarity)
	logger.Info("语义缓存命中",
		logger.StringField("event", "scache_hit"),
		logger.StringField("decision", string(decision)),
		logger.Float64Field("similarity", similarity),
		logger.StringField("scope_hash", entry.ScopeHash),
		logger.StringField("entry_id", entry.ID),
		logger.Int64Field("entry_age_s", int64(result.EntryAge/time.Second)))

	if mode == ModeShadow {
		result.ShadowDecision = decision
		result.Decision = MissShadowed
		result.Answer = ""
		obs.RecordSCacheShadow(ctx, string(decision), similarity)
	}
	return result
}

func (c *Cache) miss(ctx context.Context, result LookupResult, decision Decision) LookupResult {
	result.Decision = decision
	result.Answer = ""
	obs.RecordSCacheLookup(ctx, string(decision))
	return result
}

func (c *Cache) effectiveMode(mode Mode) Mode {
	if mode == ModeOff || mode == ModeShadow || mode == ModeOn {
		return mode
	}
	return c.mode
}

// exactLookup 先查本地索引，未加载的 scope 再查一次 Redis 的精确索引。
//
// 精确层单独一个 Hash：冷启动/重启后的第一个请求只需 HGET 两次
// （normQueryHash → entryID → Entry），不必先付一次 embedding。
// 本地索引加载完成后它就是权威数据，后续 miss 不再多一次 RTT。
func (c *Cache) exactLookup(ctx context.Context, scopeHash, normQuery string, now time.Time) *Entry {
	if entry := c.idx.exact(scopeHash, normQuery, now.Unix()); entry != nil {
		return entry
	}
	if c.scopeLoaded(scopeHash) {
		return nil
	}
	redis := c.redisClient()
	if redis == nil {
		return nil
	}

	lookupCtx, cancel := context.WithTimeout(ctx, redisColdLoadTimeout)
	defer cancel()
	id, err := redis.HGet(lookupCtx, exactKey(scopeHash), NormalizeHash(normQuery))
	if err != nil || id == "" {
		return nil
	}
	raw, err := redis.HGet(lookupCtx, bucketKey(scopeHash), id)
	if err != nil || raw == "" {
		return nil
	}
	entry, err := unmarshalEntry([]byte(raw))
	if err != nil || !entry.Valid(scopeHash, 0) || entry.Expired(now.Unix()) {
		return nil
	}
	for _, evicted := range c.idx.insert(entry) {
		c.removePersisted(lookupCtx, evicted.ScopeHash, []*Entry{evicted})
	}
	return entry
}

func (c *Cache) scopeLoaded(scopeHash string) bool {
	c.loadMu.Lock()
	defer c.loadMu.Unlock()
	return c.loaded[scopeHash].loaded
}

func (c *Cache) embed(ctx context.Context, scopeHash, text string, precomputed []float32, fn EmbedFunc) ([]float32, error) {
	if len(precomputed) > 0 {
		return precomputed, nil
	}
	if fn == nil {
		return nil, errNoEmbedding
	}
	key := scopeHash + ":" + NormalizeHash(Normalize(text))
	vec, _, err := c.embeds.Do(ctx, key, func(fillCtx context.Context) ([]float32, error) {
		return c.embedOnce(fillCtx, text, fn)
	})
	return vec, err
}

func (c *Cache) embedOnce(ctx context.Context, text string, fn EmbedFunc) ([]float32, error) {
	if !c.embedCB.allow(c.cfg.Now()) {
		return nil, errBreakerOpen
	}
	embedCtx, cancel := context.WithTimeout(ctx, c.cfg.EmbedTimeout)
	defer cancel()
	vec, err := fn(embedCtx, text)
	if err != nil {
		c.embedCB.failure(c.cfg.Now())
		return nil, err
	}
	if len(vec) == 0 {
		c.embedCB.failure(c.cfg.Now())
		return nil, errors.New("scache: empty embedding")
	}
	c.embedCB.success()
	return vec, nil
}

func (c *Cache) judge(ctx context.Context, query string, cands []Candidate) Verdict {
	if !c.cfg.JudgeEnabled || c.cfg.Judge == nil {
		return JudgeDisabled
	}
	if !c.judgeCB.allow(c.cfg.Now()) {
		return JudgeTimeout
	}
	judgeCtx, cancel := context.WithTimeout(ctx, c.cfg.JudgeTimeout)
	defer cancel()
	raw, err := c.cfg.Judge(judgeCtx, BuildJudgePrompt(query, cands))
	if err != nil {
		c.judgeCB.failure(c.cfg.Now())
		if errors.Is(judgeCtx.Err(), context.DeadlineExceeded) {
			return JudgeTimeout
		}
		return JudgeError
	}
	c.judgeCB.success()
	return ParseVerdict(raw)
}

// Store 做准入检查后把写入丢进有界队列。
//
// 队列满就丢：缓存是可选优化，让写缓存阻塞一次已经生成完的对话响应，
// 是用主链路的可用性换一个可选项的完整性，方向反了。
func (c *Cache) Store(ctx context.Context, req StoreRequest) {
	if c == nil || c.closed.Load() {
		return
	}
	mode := c.effectiveMode(req.Mode)
	if mode == ModeOff || req.Query == "" || req.Answer == "" {
		obs.RecordSCacheStore(ctx, "rejected_disabled")
		return
	}
	if err := req.Scope.Validate(); err != nil {
		obs.RecordSCacheStore(ctx, "rejected_scope")
		return
	}
	nq := Normalize(req.Query)
	if nq == "" {
		obs.RecordSCacheStore(ctx, "rejected_empty")
		return
	}
	slots := Extract(req.Query)
	if slots.Empty() && TooShortOrAnaphoric(nq) {
		obs.RecordSCacheStore(ctx, "rejected_no_slots")
		return
	}
	if reason := c.admissible(req, slots); reason != "" {
		obs.RecordSCacheStore(ctx, "rejected_"+reason)
		return
	}

	now := c.cfg.Now()
	ttl, ttlClass := c.ttlFor(req, slots)
	// TTL 加 0~10% 抖动：避免同一批条目在同一秒集中过期，
	// 把一次 miss 洪峰变成一次有界的回源。
	ttl += time.Duration(float64(ttl) * 0.1 * c.cfg.Rand())

	entry := &Entry{
		ID:        NewEntryID(),
		Query:     req.Query,
		NormQuery: nq,
		Slots:     slots,
		Answer:    req.Answer,
		Kind:      AnswerFinal,
		Vec:       req.Vector,
		Dim:       len(req.Vector),
		EmbedKey:  req.Scope.EmbedKey,
		ScopeHash: req.Scope.Hash(),
		CreatedAt: now.Unix(),
		ExpireAt:  now.Add(ttl).Unix(),
		Source:    req.Source,
	}
	obs.RecordSCacheStore(ctx, "ttl_"+ttlClass)

	select {
	case c.writeCh <- writeJob{entry: entry, scope: req.Scope, embed: req.Embed, text: req.Query}:
		obs.RecordSCacheStore(ctx, "queued")
	case <-c.stopCh:
		obs.RecordSCacheStore(ctx, "rejected_closed")
	default:
		obs.RecordSCacheStore(ctx, "dropped_queue_full")
	}
}

func (c *Cache) admissible(req StoreRequest, slots SlotSet) string {
	if req.ToolCalled {
		return "tool_turn"
	}
	if req.InjectionGuardHit {
		return "injection_guard"
	}
	if req.Source == "planner" {
		return "planner"
	}
	if req.Temperature > 0 {
		return "temperature"
	}
	length := utf8.RuneCountInString(req.Answer)
	if length < c.cfg.MinAnswerRunes || length > c.cfg.MaxAnswerRunes {
		return "answer_length"
	}
	if degradedAnswerPattern.MatchString(req.Answer) {
		return "degraded"
	}
	if personalDataPattern.MatchString(req.Query) {
		return "personal"
	}
	// 带相对时间的答案今天有效、明天就可能错，直接不入库。
	if slots.TimeRef != "" {
		return "time_sensitive"
	}
	return ""
}

// ttlFor 按内容时效性分档：价格/库存 30 分钟，其余 24 小时。
// 返回的 class 只用于指标，避免线上无法判断某批条目为什么提前过期。
func (c *Cache) ttlFor(req StoreRequest, slots SlotSet) (time.Duration, string) {
	if slots.Intent == "price" || priceSensitivePattern.MatchString(req.Query) {
		return c.cfg.PriceTTL, "price"
	}
	return c.cfg.EntryTTL, "default"
}

func (c *Cache) writeLoop() {
	defer c.wg.Done()
	for {
		select {
		case <-c.stopCh:
			return
		case job := <-c.writeCh:
			c.persistJob(job)
		}
	}
}

func (c *Cache) persistJob(job writeJob) {
	ctx, cancel := context.WithTimeout(context.Background(), redisPersistTimeout)
	defer cancel()

	entry := job.entry
	if len(entry.Vec) == 0 {
		vec, err := c.embed(ctx, entry.ScopeHash, job.text, nil, job.embed)
		if err != nil {
			obs.RecordSCacheStore(ctx, "embed_error")
			return
		}
		entry.Vec = vec
		entry.Dim = len(vec)
	}
	if !entry.Valid(entry.ScopeHash, 0) {
		obs.RecordSCacheStore(ctx, "invalid_entry")
		return
	}

	now := c.cfg.Now()
	if entry.ExpireAt <= now.Unix() {
		obs.RecordSCacheStore(ctx, "expired")
		return
	}

	// 同一 scope + 同一问题串行化：多个 writer 同时处理相同问题时，
	// 「删旧 + 插新 + 落 Redis」必须是一个整体，否则后写的旧条目会把
	// 先写的新条目从索引里删掉，造成本地与 Redis 分叉（D16）。
	lock := c.writeLock(entry.ScopeHash, entry.NormQuery)
	lock.Lock()
	defer lock.Unlock()

	// 同一归一化问题只保留最新一条，否则反复 miss 会让 scope 被同一问题填满。
	if old := c.idx.exact(entry.ScopeHash, entry.NormQuery, now.Unix()); old != nil && old.ID != entry.ID {
		c.idx.remove(entry.ScopeHash, old.ID)
		c.removePersisted(ctx, entry.ScopeHash, []*Entry{old})
	}

	for _, evicted := range c.idx.insert(entry) {
		c.removePersisted(ctx, evicted.ScopeHash, []*Entry{evicted})
	}

	redis := c.redisClient()
	if redis == nil {
		obs.RecordSCacheStore(ctx, "local_only")
		return
	}
	if err := c.persist(ctx, redis, entry, job.scope); err != nil {
		c.noteRedisFailure()
		obs.RecordSCacheStore(ctx, "persist_error")
		return
	}
	obs.RecordSCacheStore(ctx, "ok")
}

func (c *Cache) writeLock(scopeHash, normQuery string) *sync.Mutex {
	h := fnv.New32a()
	_, _ = h.Write([]byte(scopeHash))
	_, _ = h.Write([]byte{0x1f})
	_, _ = h.Write([]byte(normQuery))
	return &c.writeLocks[int(h.Sum32())%len(c.writeLocks)]
}

// persistScript 一次 RTT 完成条目、索引、精确层、meta 与 TTL。
// 半写状态会让冷启动读到「有 Hash 无 ZSet」的孤儿，因此不拆成多条命令。
var persistScript = `
redis.call('HSET', KEYS[1], ARGV[1], ARGV[2])
redis.call('ZADD', KEYS[2], ARGV[3], ARGV[1])
redis.call('HSET', KEYS[3], ARGV[5], ARGV[1])
redis.call('HSET', KEYS[4],
	'tenant', ARGV[6], 'bot', ARGV[7], 'user', ARGV[8],
	'kb', ARGV[9], 'prompt', ARGV[10], 'model', ARGV[11],
	'embed', ARGV[12], 'created_at', ARGV[3])
redis.call('EXPIRE', KEYS[1], ARGV[4])
redis.call('EXPIRE', KEYS[2], ARGV[4])
redis.call('EXPIRE', KEYS[3], ARGV[14])
redis.call('EXPIRE', KEYS[4], ARGV[13])
redis.call('SADD', KEYS[5], ARGV[15])
redis.call('EXPIRE', KEYS[5], ARGV[13])
return 1
`

func (c *Cache) persist(ctx context.Context, redis cache.Cache, entry *Entry, scope Scope) error {
	raw, err := entry.marshal()
	if err != nil {
		return err
	}
	ttl := entry.ExpireAt - c.cfg.Now().Unix()
	if ttl < 1 {
		ttl = 1
	}
	// 精确层只服务「归一化后完全相同」的查询，没必要和向量条目同寿。
	// 配置更短时提前回收，配置更长时也不能超过条目本身的有效期。
	exactTTL := min(ttl, int64(c.cfg.ExactTTL/time.Second))
	if exactTTL < 1 {
		exactTTL = 1
	}
	metaTTL := ttl * 2
	_, err = redis.EvalSha(ctx, persistScript,
		[]string{
			bucketKey(entry.ScopeHash),
			indexKey(entry.ScopeHash),
			exactKey(entry.ScopeHash),
			metaKey(entry.ScopeHash),
			userKey(scope.UserID),
		},
		entry.ID, string(raw), entry.CreatedAt, ttl, NormalizeHash(entry.NormQuery),
		scope.Tenant, scope.BotID, scope.UserID, scope.KBVersion,
		scope.PromptHash, scope.ModelKey, scope.EmbedKey,
		metaTTL, exactTTL, entry.ScopeHash)
	return err
}

func (c *Cache) removePersisted(ctx context.Context, scopeHash string, entries []*Entry) {
	if len(entries) == 0 {
		return
	}
	redis := c.redisClient()
	if redis == nil {
		return
	}
	ids := make([]string, 0, len(entries))
	exactFields := make([]string, 0, len(entries))
	for _, entry := range entries {
		ids = append(ids, entry.ID)
		exactFields = append(exactFields, NormalizeHash(entry.NormQuery))
	}
	if err := redis.HDel(ctx, bucketKey(scopeHash), ids); err != nil {
		c.noteRedisFailure()
		return
	}
	members := make([]interface{}, len(ids))
	for i, id := range ids {
		members[i] = id
	}
	if err := redis.ZRem(ctx, indexKey(scopeHash), members...); err != nil {
		c.noteRedisFailure()
	}
	if err := redis.HDel(ctx, exactKey(scopeHash), exactFields); err != nil {
		c.noteRedisFailure()
	}
}

func (c *Cache) evictLoop() {
	defer c.wg.Done()
	ticker := time.NewTicker(c.cfg.EvictInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			c.evictOnce()
		case <-c.stopCh:
			return
		}
	}
}

// evictOnce 一次多删 20%，避免每分钟都触发淘汰（§12.4）。
func (c *Cache) evictOnce() {
	const ratio = 0.8
	total, bytes := c.idx.stats()
	if total <= c.cfg.MaxTotalEntries && bytes <= c.cfg.MaxTotalBytes {
		return
	}
	targetEntries := int(float64(c.cfg.MaxTotalEntries) * ratio)
	targetBytes := int64(float64(c.cfg.MaxTotalBytes) * ratio)

	ctx, cancel := context.WithTimeout(context.Background(), redisPersistTimeout)
	defer cancel()

	removed := 0
	for {
		total, bytes = c.idx.stats()
		if total <= targetEntries && bytes <= targetBytes {
			break
		}
		oldest := c.idx.oldest()
		if oldest == nil {
			break
		}
		c.idx.remove(oldest.ScopeHash, oldest.ID)
		c.removePersisted(ctx, oldest.ScopeHash, []*Entry{oldest})
		removed++
	}
	if removed > 0 {
		obs.RecordSCacheEvict(ctx, removed)
	}
	obs.RecordSCacheEntries(ctx, total)
}

// ensureLoaded 在 scope 首次访问时从 Redis 恢复索引。
//
// 它在 embedding 之后调用：冷启动的第一条请求本来就要算向量，
// 不能为了「可能命中」先付一次 Redis 连接/读取的代价。后续请求全部
// 走本地索引，Redis 只负责重启恢复。
func (c *Cache) ensureLoaded(ctx context.Context, scopeHash string, now time.Time) {
	redis := c.redisClient()
	if redis == nil {
		return
	}

	c.loadMu.Lock()
	state := c.loaded[scopeHash]
	if state.loaded || state.loading {
		c.loadMu.Unlock()
		return
	}
	if !state.at.IsZero() && now.Sub(state.at) < c.cfg.LoadRetry {
		c.loadMu.Unlock()
		return
	}
	state.loading = true
	c.loaded[scopeHash] = state
	c.loadMu.Unlock()

	entries, err := c.loadScope(ctx, redis, scopeHash, now)

	c.loadMu.Lock()
	state = c.loaded[scopeHash]
	state.loading = false
	state.at = now
	state.loaded = err == nil
	c.loaded[scopeHash] = state
	c.loadMu.Unlock()

	if err != nil {
		c.noteRedisFailure()
		obs.RecordSCacheColdStart(ctx, "fail")
		return
	}
	for _, entry := range entries {
		for _, evicted := range c.idx.insert(entry) {
			c.removePersisted(ctx, evicted.ScopeHash, []*Entry{evicted})
		}
	}
	obs.RecordSCacheColdStart(ctx, "ok")
}

func (c *Cache) loadScope(ctx context.Context, redis cache.Cache, scopeHash string, now time.Time) ([]*Entry, error) {
	loadCtx, cancel := context.WithTimeout(ctx, redisColdLoadTimeout)
	defer cancel()

	fields, err := redis.HGetAll(loadCtx, bucketKey(scopeHash))
	if err != nil {
		return nil, err
	}
	out := make([]*Entry, 0, len(fields))
	for _, raw := range fields {
		entry, err := unmarshalEntry([]byte(raw))
		if err != nil {
			continue
		}
		if !entry.Valid(scopeHash, 0) || entry.Expired(now.Unix()) {
			continue
		}
		out = append(out, entry)
	}
	return out, nil
}

// redisClient 返回当前可用的 Redis 句柄。
//
// 首次解析放到后台：pkg/cache 初次连不上时会重试十几秒，缓存不能把
// 请求路径卡在「建立连接」上。解析完成前一律按无 Redis 处理。
func (c *Cache) redisClient() cache.Cache {
	if c == nil || c.cfg.CacheProvider == nil {
		return nil
	}
	now := c.cfg.Now()
	if !c.redisCB.allow(now) {
		return nil
	}
	c.redisMu.Lock()
	defer c.redisMu.Unlock()
	if c.redis != nil {
		return c.redis
	}
	if c.redisResolving || now.Before(c.redisNextTry) {
		return nil
	}
	c.redisResolving = true
	go c.resolveRedis()
	return nil
}

// PrimeRedis 同步解析 Redis 句柄，供启动预热与测试使用。
func (c *Cache) PrimeRedis() cache.Cache {
	if c == nil || c.cfg.CacheProvider == nil {
		return nil
	}
	c.redisMu.Lock()
	if c.redis != nil {
		redis := c.redis
		c.redisMu.Unlock()
		return redis
	}
	if c.redisResolving {
		c.redisMu.Unlock()
		return nil
	}
	c.redisResolving = true
	c.redisMu.Unlock()
	return c.resolveRedis()
}

func (c *Cache) resolveRedis() cache.Cache {
	ctx, cancel := context.WithTimeout(context.Background(), redisPersistTimeout)
	defer cancel()

	redis := c.cfg.CacheProvider()

	c.redisMu.Lock()
	c.redisResolving = false
	if redis != nil {
		c.redis = redis
	} else {
		c.redisNextTry = c.cfg.Now().Add(redisRetryAfterFail)
	}
	c.redisMu.Unlock()

	if redis == nil {
		c.redisCB.failure(c.cfg.Now())
		obs.RecordSCacheRedis(ctx, "unavailable")
		return nil
	}
	c.redisCB.success()
	obs.RecordSCacheRedis(ctx, "ok")
	return redis
}

func (c *Cache) noteRedisFailure() {
	now := c.cfg.Now()
	c.redisCB.failure(now)
	c.redisMu.Lock()
	c.redisNextTry = now.Add(redisRetryAfterFail)
	c.redisMu.Unlock()
}

// KBVersion 返回知识库版本号。
//
// 版本号进入 ScopeHash：入库新文档后旧答案在物理上不可达。本地缓存
// 10 秒减少跨实例轮询开销，多实例的失效窗口因此不超过 10 秒；
// 单实例下 BumpKBVersion 是立即生效的。
func (c *Cache) KBVersion(ctx context.Context, botID string) string {
	if c == nil || botID == "" {
		return "0"
	}
	now := c.cfg.Now()
	c.verMu.Lock()
	state, ok := c.versions[botID]
	c.verMu.Unlock()
	if ok && now.Sub(state.at) < kbVersionLocalTTL {
		return strconv.FormatInt(state.version, 10)
	}

	var version int64
	if redis := c.redisClient(); redis != nil {
		if raw, err := redis.Get(ctx, fmt.Sprintf(kbVersionKey, botID)); err == nil {
			if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil {
				version = parsed
			}
		}
	}
	if ok && state.version > version {
		version = state.version
	}
	c.verMu.Lock()
	c.versions[botID] = versionState{version: version, at: now}
	c.verMu.Unlock()
	return strconv.FormatInt(version, 10)
}

// BumpKBVersion 在知识库写入成功后推进版本号。
// Redis 不可用时只推进本实例：至少保证当前进程不会继续命中旧答案。
func (c *Cache) BumpKBVersion(ctx context.Context, botID string) int64 {
	if c == nil || botID == "" {
		return 0
	}
	now := c.cfg.Now()
	c.verMu.Lock()
	state := c.versions[botID]
	state.version++
	state.at = now
	c.versions[botID] = state
	local := state.version
	c.verMu.Unlock()

	if redis := c.redisClient(); redis != nil {
		if version, err := redis.Incr(ctx, fmt.Sprintf(kbVersionKey, botID)); err == nil {
			c.verMu.Lock()
			c.versions[botID] = versionState{version: version, at: now}
			c.verMu.Unlock()
			return version
		}
		c.noteRedisFailure()
	}
	return local
}

// ReplayStream 把整段答案按配置的节奏回放给流式调用方。
func (c *Cache) ReplayStream(answer string, onChunk func(string) error) error {
	if c == nil {
		return nil
	}
	return ReplayAsStream(answer, c.cfg.ReplayChunkRunes, c.cfg.ReplayBudget, c.cfg.ReplayMaxDelay, onChunk)
}

// ForgetUser 按用户删除其全部缓存。
//
// 不允许按 UserID 扫全库：删除时用 meta 里的 user → scopeHash 反向索引
// 定位，元素量级是个位数。每个 scope 只属于一个用户（UserID 在 ScopeHash
// 里），因此删 scope 不会误伤其他用户。
func (c *Cache) ForgetUser(ctx context.Context, userID string) (int, error) {
	if c == nil || userID == "" {
		return 0, nil
	}
	redis := c.redisClient()
	if redis == nil {
		return 0, errNoRedis
	}
	scopes, err := redis.SMembers(ctx, userKey(userID))
	if err != nil {
		c.noteRedisFailure()
		return 0, err
	}
	deleted := 0
	for _, scopeHash := range scopes {
		for _, key := range []string{
			bucketKey(scopeHash),
			indexKey(scopeHash),
			exactKey(scopeHash),
			metaKey(scopeHash),
		} {
			if err := redis.Delete(ctx, key); err != nil {
				c.noteRedisFailure()
				return deleted, err
			}
		}
		c.idx.removeScope(scopeHash)
		deleted++
	}
	if err := redis.Delete(ctx, userKey(userID)); err != nil {
		c.noteRedisFailure()
		return deleted, err
	}
	obs.RecordSCacheEvict(ctx, deleted)
	logger.Info("语义缓存按用户删除",
		logger.StringField("event", "scache_forget_user"),
		logger.StringField("user_id", userID),
		logger.IntField("scopes", deleted))
	return deleted, nil
}

// Stats 返回本地索引的条目数与估算字节数，供监控与测试使用。
func (c *Cache) Stats() (entries int, bytes int64) {
	if c == nil {
		return 0, 0
	}
	return c.idx.stats()
}

// Close 停止后台 worker。重复调用安全。
func (c *Cache) Close() {
	if c == nil || !c.closed.CompareAndSwap(false, true) {
		return
	}
	close(c.stopCh)
	c.wg.Wait()
}

func bucketKey(scopeHash string) string {
	return redisKeyPrefix + ":" + scopeHash
}

func indexKey(scopeHash string) string {
	return redisKeyPrefix + ":" + scopeHash + ":idx"
}

func exactKey(scopeHash string) string {
	return redisKeyPrefix + ":" + scopeHash + ":exact"
}

func metaKey(scopeHash string) string {
	return redisKeyPrefix + ":meta:" + scopeHash
}

func userKey(userID string) string {
	return redisKeyPrefix + ":user:" + userID
}
