package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"Logos/internal/service/ai/recommend/dao"
	"Logos/internal/service/ai/recommend/model"
	"Logos/pkg/cache"
	"Logos/pkg/singleflight"
)

// fakeRecommendCache 只实现 Get/Set，其余方法由嵌入的 nil 接口兜底。
// 测试只走缓存命中/写回路径，不会调用其他方法。
type fakeRecommendCache struct {
	cache.Cache
	mu    sync.Mutex
	store map[string]string
	gets  int32
	sets  int32
	// lastTTL 记录最近一次写入的过期时间，用来区分正常缓存与负缓存。
	lastTTL time.Duration
}

func newFakeRecommendCache() *fakeRecommendCache {
	return &fakeRecommendCache{store: make(map[string]string)}
}

func (f *fakeRecommendCache) Get(ctx context.Context, key string) (string, error) {
	atomic.AddInt32(&f.gets, 1)
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.store[key], nil
}

func (f *fakeRecommendCache) Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
	atomic.AddInt32(&f.sets, 1)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.store[key] = value.(string)
	f.lastTTL = expiration
	return nil
}

type fakeVectorService struct {
	VectorService
	err   error
	ids   []string
	calls int32
}

func (f *fakeVectorService) SearchSimilar(ctx context.Context, text string, topK int) ([]string, error) {
	atomic.AddInt32(&f.calls, 1)
	if f.err != nil {
		return nil, f.err
	}
	return f.ids, nil
}

type fakeRecommendRepo struct {
	dao.RecommendRepository
	calls       int32
	inFlight    int32
	maxInFlight int32
	delay       time.Duration
	items       []*model.RecommendationItem
}

func (f *fakeRecommendRepo) GetRecommendations(ctx context.Context, userID int64, recommendType string, limit int) ([]*model.RecommendationItem, int64, error) {
	atomic.AddInt32(&f.calls, 1)
	current := atomic.AddInt32(&f.inFlight, 1)
	for {
		max := atomic.LoadInt32(&f.maxInFlight)
		if current <= max || atomic.CompareAndSwapInt32(&f.maxInFlight, max, current) {
			break
		}
	}
	defer atomic.AddInt32(&f.inFlight, -1)
	time.Sleep(f.delay)
	return f.items, int64(len(f.items)), nil
}

func TestGetRecommendations_MergesConcurrentCacheMiss(t *testing.T) {
	c := newFakeRecommendCache()
	repo := &fakeRecommendRepo{
		delay: 20 * time.Millisecond,
		items: []*model.RecommendationItem{{ID: "a"}, {ID: "b"}},
	}
	svc := &recommendServiceImpl{
		recommendRepo: repo,
		cache:         c,
		fills:         singleflight.NewGroup[recommendResult](time.Second),
	}

	var wg sync.WaitGroup
	const n = 30
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			items, total, err := svc.GetRecommendations(context.Background(), 7, "feed", 10, nil)
			if err != nil {
				t.Errorf("GetRecommendations 失败: %v", err)
				return
			}
			if len(items) != 2 || total != 2 {
				t.Errorf("结果不完整: len=%d total=%d", len(items), total)
			}
		}()
	}
	wg.Wait()

	// 缓存为空时 30 个并发请求只应触发 1 次回源。
	if got := atomic.LoadInt32(&repo.calls); got != 1 {
		t.Fatalf("并发 miss 只应回源 1 次，实际 %d 次", got)
	}
}

func TestGetRecommendations_CacheKeyUsesRequestedLimit(t *testing.T) {
	c := newFakeRecommendCache()
	repo := &fakeRecommendRepo{
		// 数据库只有 3 条，但请求要 10 条。
		items: []*model.RecommendationItem{{ID: "a"}, {ID: "b"}, {ID: "c"}},
	}
	svc := &recommendServiceImpl{
		recommendRepo: repo,
		cache:         c,
		fills:         singleflight.NewGroup[recommendResult](time.Second),
	}

	if _, _, err := svc.GetRecommendations(context.Background(), 7, "feed", 10, nil); err != nil {
		t.Fatalf("首次请求失败: %v", err)
	}
	// 写回的 key 必须是 limit:10，而不是按实际条数写成 limit:3。
	if _, ok := c.store["recommend:7:feed:limit:10"]; !ok {
		t.Fatalf("写回的 key 应按请求 limit 生成，实际 store keys=%v", keysOf(c.store))
	}

	before := atomic.LoadInt32(&repo.calls)
	if _, _, err := svc.GetRecommendations(context.Background(), 7, "feed", 10, nil); err != nil {
		t.Fatalf("第二次请求失败: %v", err)
	}
	if atomic.LoadInt32(&repo.calls) != before {
		t.Fatal("第二次请求应命中缓存，不应再查库")
	}
}

// 空结果不缓存时，没有推荐数据的用户每次刷新都会重跑
// 「查库 → 向量检索 → 知识图谱检索 → 逐条 LLM 改写」。
func TestGetRecommendations_EmptyResultUsesShortNegativeCache(t *testing.T) {
	c := newFakeRecommendCache()
	repo := &fakeRecommendRepo{}
	svc := &recommendServiceImpl{
		recommendRepo: repo,
		cache:         c,
		fills:         singleflight.NewGroup[recommendResult](time.Second),
	}

	for i := 0; i < 3; i++ {
		items, total, err := svc.GetRecommendations(context.Background(), 9, "feed", 10, nil)
		if err != nil || len(items) != 0 || total != 0 {
			t.Fatalf("第 %d 次请求 items=%v total=%d err=%v", i, items, total, err)
		}
	}
	if got := atomic.LoadInt32(&repo.calls); got != 1 {
		t.Fatalf("空结果应写负缓存，后续请求不再回源；实际回源 %d 次", got)
	}
	if c.lastTTL <= 0 || c.lastTTL > 2*time.Minute {
		t.Fatalf("负缓存 TTL 必须远短于正常结果，实际 %v", c.lastTTL)
	}
}

// 依赖失败得到的空列表只是降级，不是「用户没有推荐」，
// 写进负缓存会让依赖恢复后仍被挡在 TTL 外面。
func TestGetRecommendations_DegradedEmptyResultNotCached(t *testing.T) {
	c := newFakeRecommendCache()
	repo := &fakeRecommendRepo{}
	svc := &recommendServiceImpl{
		recommendRepo: repo,
		cache:         c,
		vectorService: &fakeVectorService{err: errors.New("milvus unavailable")},
		fills:         singleflight.NewGroup[recommendResult](time.Second),
	}

	for i := 0; i < 2; i++ {
		if _, _, err := svc.GetRecommendations(context.Background(), 11, "feed", 10, nil); err != nil {
			t.Fatalf("第 %d 次请求失败: %v", i, err)
		}
	}
	if got := atomic.LoadInt32(&repo.calls); got != 2 {
		t.Fatalf("依赖失败的空结果不得写负缓存，第二次应重新回源；实际回源 %d 次", got)
	}
	if got := atomic.LoadInt32(&c.sets); got != 0 {
		t.Fatalf("依赖失败时不应写缓存，实际 Set %d 次", got)
	}
}

func keysOf(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func TestBatchGetRecommendations_BoundedConcurrency(t *testing.T) {
	c := newFakeRecommendCache()
	repo := &fakeRecommendRepo{
		delay: 20 * time.Millisecond,
		items: []*model.RecommendationItem{{ID: "a"}},
	}
	svc := &recommendServiceImpl{
		recommendRepo: repo,
		cache:         c,
		fills:         singleflight.NewGroup[recommendResult](time.Second),
	}

	userIDs := make([]int64, 0, 24)
	for i := int64(1); i <= 24; i++ {
		userIDs = append(userIDs, i)
	}

	got, err := svc.BatchGetRecommendations(context.Background(), userIDs, "feed", 10)
	if err != nil {
		t.Fatalf("BatchGetRecommendations 失败: %v", err)
	}
	if len(got) != len(userIDs) {
		t.Fatalf("返回用户数 = %d, want %d", len(got), len(userIDs))
	}
	if max := atomic.LoadInt32(&repo.maxInFlight); max > recommendationBatchConcurrency {
		t.Fatalf("并发度 %d 超过上限 %d，会打满连接池", max, recommendationBatchConcurrency)
	}
	if max := atomic.LoadInt32(&repo.maxInFlight); max < 2 {
		t.Fatalf("批量接口应并发回源（观测到的最大并发 %d），否则就是串行 N 次往返", max)
	}
}

func TestBatchGetRecommendations_DuplicateIDsMerged(t *testing.T) {
	c := newFakeRecommendCache()
	repo := &fakeRecommendRepo{items: []*model.RecommendationItem{{ID: "a"}}}
	svc := &recommendServiceImpl{
		recommendRepo: repo,
		cache:         c,
		fills:         singleflight.NewGroup[recommendResult](time.Second),
	}

	got, err := svc.BatchGetRecommendations(context.Background(), []int64{7, 7, 7}, "feed", 10)
	if err != nil {
		t.Fatalf("BatchGetRecommendations 失败: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("重复 userID 应合并成一个结果，实际 %d 个", len(got))
	}
	if calls := atomic.LoadInt32(&repo.calls); calls != 1 {
		t.Fatalf("重复 userID 只应回源 1 次，实际 %d 次", calls)
	}
}
