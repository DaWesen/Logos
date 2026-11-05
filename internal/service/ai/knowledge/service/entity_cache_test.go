package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"Logos/internal/service/ai/knowledge/dao"
	"Logos/internal/service/ai/knowledge/model"
	"Logos/pkg/cache"
	"Logos/pkg/singleflight"
)

// fakeEntityCache 只实现实体缓存用到的四种操作，其余方法由嵌入的 nil 接口兜底。
type fakeEntityCache struct {
	cache.Cache
	mu      sync.Mutex
	values  map[string]string
	deletes int32
	sets    int32
}

func newFakeEntityCache() *fakeEntityCache {
	return &fakeEntityCache{values: make(map[string]string)}
}

func (f *fakeEntityCache) Get(ctx context.Context, key string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.values[key], nil
}

func (f *fakeEntityCache) Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
	atomic.AddInt32(&f.sets, 1)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.values[key] = value.(string)
	return nil
}

func (f *fakeEntityCache) Delete(ctx context.Context, key string) error {
	atomic.AddInt32(&f.deletes, 1)
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.values, key)
	return nil
}

func (f *fakeEntityCache) Exists(ctx context.Context, key string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.values[key]
	return ok, nil
}

type fakeEntityRepo struct {
	dao.KnowledgeRepository
	calls int32
	delay time.Duration
	// byID 为空表示该 ID 不存在。
	byID    map[string]*model.Entity
	findErr error
	txCalls int32
}

func (f *fakeEntityRepo) GetEntity(ctx context.Context, id string) (*model.Entity, error) {
	atomic.AddInt32(&f.calls, 1)
	time.Sleep(f.delay)
	return f.byID[id], nil
}

func (f *fakeEntityRepo) FindEntityByNameAndType(context.Context, string, string, string, string) (*model.Entity, error) {
	return nil, f.findErr
}

func (f *fakeEntityRepo) WithTransaction(context.Context, func(dao.KnowledgeRepository) error) error {
	atomic.AddInt32(&f.txCalls, 1)
	return nil
}

func newEntityServiceForTest(c *fakeEntityCache, repo *fakeEntityRepo) *knowledgeServiceImpl {
	return &knowledgeServiceImpl{
		repo:        repo,
		cache:       c,
		entityFills: singleflight.NewGroup[*model.Entity](time.Second),
	}
}

func TestGetEntity_MergesConcurrentMiss(t *testing.T) {
	c := newFakeEntityCache()
	repo := &fakeEntityRepo{
		delay: 20 * time.Millisecond,
		byID:  map[string]*model.Entity{"e1": {ID: "e1", Name: "Logos"}},
	}
	svc := newEntityServiceForTest(c, repo)

	var wg sync.WaitGroup
	const n = 30
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			entity, err := svc.GetEntity(context.Background(), "e1")
			if err != nil || entity == nil {
				t.Errorf("GetEntity 失败: entity=%v err=%v", entity, err)
			}
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt32(&repo.calls); got != 1 {
		t.Fatalf("并发未命中只应回源 1 次，实际 %d 次", got)
	}
}

func TestGetEntity_CachesMissingEntity(t *testing.T) {
	c := newFakeEntityCache()
	repo := &fakeEntityRepo{byID: map[string]*model.Entity{}}
	svc := newEntityServiceForTest(c, repo)

	for i := 0; i < 5; i++ {
		if _, err := svc.GetEntity(context.Background(), "not-exist"); err != ErrEntityNotFound {
			t.Fatalf("第 %d 次查询 err = %v, want ErrEntityNotFound", i, err)
		}
	}
	if got := atomic.LoadInt32(&repo.calls); got != 1 {
		t.Fatalf("不存在的实体只应查库 1 次（空值缓存），实际 %d 次", got)
	}
}

func TestCacheEntity_ClearsMissingMarker(t *testing.T) {
	c := newFakeEntityCache()
	svc := newEntityServiceForTest(c, &fakeEntityRepo{})

	svc.cacheMissingEntity(context.Background(), "e2")
	if !svc.isMissingEntityCached(context.Background(), "e2") {
		t.Fatal("空值标记应已写入")
	}
	svc.cacheEntity(context.Background(), &model.Entity{ID: "e2", Name: "new"})
	if svc.isMissingEntityCached(context.Background(), "e2") {
		t.Fatal("实体写回后必须清掉空值标记，否则新实体 60s 内查不到")
	}
}

// 序列化失败时旧实现会把空串写进缓存：Set 返回成功，key 被占到 TTL 结束，
// 读侧又只能按未命中处理。更新实体时更糟——缓存里会留下旧版本。
func TestCacheEntity_MarshalFailureDoesNotPoisonCache(t *testing.T) {
	ctx := context.Background()
	c := newFakeEntityCache()
	svc := newEntityServiceForTest(c, &fakeEntityRepo{})

	svc.cacheEntity(ctx, &model.Entity{ID: "e3", Name: "old"})
	if ok, _ := c.Exists(ctx, "entity:e3"); !ok {
		t.Fatal("前置条件失败：正常实体应已写入缓存")
	}

	orig := marshalCachedEntity
	marshalCachedEntity = func(any) ([]byte, error) { return nil, errors.New("marshal failed") }
	t.Cleanup(func() { marshalCachedEntity = orig })

	before := atomic.LoadInt32(&c.sets)
	svc.cacheEntity(ctx, &model.Entity{ID: "e3", Name: "new"})

	if got := atomic.LoadInt32(&c.sets); got != before {
		t.Fatalf("序列化失败不得写缓存，Set 调用从 %d 变成 %d", before, got)
	}
	if ok, _ := c.Exists(ctx, "entity:e3"); ok {
		t.Fatal("序列化失败必须删掉旧缓存，否则更新后仍会返回过期实体")
	}
}

// 缓存内容损坏不是「实体不存在」：必须按未命中回源，并用回源结果覆盖坏值。
func TestGetEntity_CorruptCacheFallsBackToRepo(t *testing.T) {
	ctx := context.Background()
	c := newFakeEntityCache()
	if err := c.Set(ctx, "entity:e4", "{not-json", time.Minute); err != nil {
		t.Fatalf("写入损坏缓存失败: %v", err)
	}
	repo := &fakeEntityRepo{byID: map[string]*model.Entity{"e4": {ID: "e4", Name: "Logos"}}}
	svc := newEntityServiceForTest(c, repo)

	entity, err := svc.GetEntity(ctx, "e4")
	if err != nil || entity == nil || entity.Name != "Logos" {
		t.Fatalf("损坏缓存应按未命中回源，实际 entity=%v err=%v", entity, err)
	}
	if got := atomic.LoadInt32(&repo.calls); got != 1 {
		t.Fatalf("损坏缓存应回源 1 次，实际 %d 次", got)
	}
	cached, _ := c.Get(ctx, "entity:e4")
	if cached == "" || cached == "{not-json" {
		t.Fatalf("缓存应被回源结果覆盖，实际 %q", cached)
	}
}

// 查重查询失败不等于「名称可用」。旧实现只打 Warn 就继续创建，
// 结果要么撞唯一约束，要么静默造出重名实体。
func TestFindOrCreateEntity_LookupFailureDoesNotCreate(t *testing.T) {
	repo := &fakeEntityRepo{findErr: errors.New("database unavailable")}
	svc := newEntityServiceForTest(newFakeEntityCache(), repo)

	entity, err := svc.FindOrCreateEntity(context.Background(), "PERSON", "alice", "c1", nil, nil, "")
	if !errors.Is(err, ErrInternalServer) {
		t.Fatalf("查重失败应返回 ErrInternalServer，实际 entity=%v err=%v", entity, err)
	}
	if entity != nil {
		t.Fatalf("查重失败不应返回实体，实际 %+v", entity)
	}
	if got := atomic.LoadInt32(&repo.txCalls); got != 0 {
		t.Fatalf("查重失败后不得进入创建事务，实际调用 %d 次", got)
	}
}
