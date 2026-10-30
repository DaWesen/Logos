package service

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"Logos/internal/service/messaging/im/dao"
	"Logos/internal/service/messaging/im/model"
	"Logos/pkg/cache"
	"Logos/pkg/logger"

	"go.uber.org/zap"
)

// TestMain 用 nop logger，避免 logger 初始化去读仓库根目录的 config 文件。
func TestMain(m *testing.M) {
	logger.SetLogger(zap.NewNop())
	os.Exit(m.Run())
}

// fakeCache 只实现测试用到的方法；其余方法通过嵌入 nil 接口“继承”，被调用即 panic，
// 从而保证测试不会静默依赖未验证的行为。
type fakeCache struct {
	cache.Cache

	mu       sync.Mutex
	exists   map[string]bool
	ttl      map[string]time.Duration
	setCalls int
}

func newFakeCache() *fakeCache {
	return &fakeCache{exists: make(map[string]bool), ttl: make(map[string]time.Duration)}
}

func (f *fakeCache) Set(_ context.Context, key string, _ interface{}, expiration time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setCalls++
	f.ttl[key] = expiration
	return nil
}

func (f *fakeCache) Exists(_ context.Context, key string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.exists[key], nil
}

func (f *fakeCache) Delete(_ context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.exists, key)
	delete(f.ttl, key)
	return nil
}

func (f *fakeCache) ttlOf(key string) time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ttl[key]
}

// fakeRepo 只实现测试用的查询；其余方法被调用即 panic。
type fakeRepo struct {
	dao.IMRepository

	calls  int
	online map[string]bool
}

func (f *fakeRepo) UpsertOnlineRecord(_ context.Context, _ *model.OnlineRecord) error {
	return nil
}

func (f *fakeRepo) GetBatchOnlineStatus(_ context.Context, _ []string) (map[string]bool, error) {
	f.calls++
	return f.online, nil
}

func (f *fakeRepo) SetAllUserOffline(_ context.Context, _ string) error {
	return nil
}

func newTestService(repo dao.IMRepository, c cache.Cache) *IMServiceImpl {
	return &IMServiceImpl{repo: repo, cache: c, ctx: context.Background()}
}

// TestConnectSetsExpiringOnlineCache 上线缓存必须带 TTL，否则崩溃用户会永久在线。
func TestConnectSetsExpiringOnlineCache(t *testing.T) {
	c := newFakeCache()
	svc := newTestService(&fakeRepo{}, c)

	if err := svc.Connect(context.Background(), "1001", "desktop", "s1"); err != nil {
		t.Fatalf("connect failed: %v", err)
	}

	key := onlineStatusCachePrefix + "1001"
	if got := c.ttlOf(key); got != onlineCacheTTL {
		t.Fatalf("online cache ttl = %v, want %v", got, onlineCacheTTL)
	}
}

// TestGetOnlineStatusCachesDBResultWithTTL 回填缓存同样要带 TTL。
func TestGetOnlineStatusCachesDBResultWithTTL(t *testing.T) {
	c := newFakeCache()
	repo := &fakeRepo{online: map[string]bool{"1002": true}}
	svc := newTestService(repo, c)

	status, err := svc.GetOnlineStatus(context.Background(), []string{"1002"})
	if err != nil {
		t.Fatalf("get online status failed: %v", err)
	}
	if !status["1002"] {
		t.Fatalf("1002 should be online")
	}
	if got := c.ttlOf(onlineStatusCachePrefix + "1002"); got != onlineCacheTTL {
		t.Fatalf("backfilled cache ttl = %v, want %v", got, onlineCacheTTL)
	}
}

// TestGetOnlineStatusPrefersCache 缓存命中时不再打数据库。
func TestGetOnlineStatusPrefersCache(t *testing.T) {
	c := newFakeCache()
	c.exists[onlineStatusCachePrefix+"1003"] = true
	repo := &fakeRepo{online: map[string]bool{}}
	svc := newTestService(repo, c)

	status, err := svc.GetOnlineStatus(context.Background(), []string{"1003"})
	if err != nil {
		t.Fatalf("get online status failed: %v", err)
	}
	if !status["1003"] {
		t.Fatalf("1003 should be online from cache")
	}
	if repo.calls != 0 {
		t.Fatalf("db should not be queried on cache hit, got %d calls", repo.calls)
	}
}

// TestSetOnlineStatusFalseClearsCache 主动下线要清掉缓存，避免残留正向标记。
func TestSetOnlineStatusFalseClearsCache(t *testing.T) {
	c := newFakeCache()
	c.exists[onlineStatusCachePrefix+"1004"] = true
	svc := newTestService(&fakeRepo{}, c)

	if err := svc.SetOnlineStatus(context.Background(), "1004", false); err != nil {
		t.Fatalf("set offline failed: %v", err)
	}
	if _, ok := c.exists[onlineStatusCachePrefix+"1004"]; ok {
		t.Fatalf("offline should clear cache")
	}
}
