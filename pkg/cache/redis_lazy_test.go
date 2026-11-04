package cache

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"Logos/config"
)

// TestMain 切到仓库根目录并指定测试夹具配置：NewRedisCache 会调用
// config.GetConfig()，而默认的 ./config/config.yaml 不入库。
func TestMain(m *testing.M) {
	if wd, err := os.Getwd(); err == nil {
		for dir := wd; ; dir = filepath.Dir(dir) {
			if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
				_ = os.Chdir(dir)
				_ = os.Setenv("LOGOS_CONFIG_PATH", filepath.Join(dir, "config", "testdata", "config.yaml"))
				break
			}
			if filepath.Dir(dir) == dir {
				break
			}
		}
	}
	os.Exit(m.Run())
}

type fakeCache struct {
	Cache
}

func (f *fakeCache) Get(context.Context, string) (string, error) { return "", nil }

// resetCacheState 在测试之间重置包级状态，避免测试相互污染。
func resetCacheState(t *testing.T) {
	t.Helper()
	cacheMu.Lock()
	cacheInstance = nil
	cacheRetryAfter = time.Time{}
	cacheMu.Unlock()
	t.Cleanup(func() {
		cacheMu.Lock()
		cacheInstance = nil
		cacheRetryAfter = time.Time{}
		cacheMu.Unlock()
		initRedisFunc = InitRedis
		clockNow = time.Now
	})
}

// TestNewRedisCache_RetriesAfterCooldown
// 这是 sync.Once 版本最致命的问题：启动时 Redis 没起来，
// 之后恢复了，进程也必须重启才能用上缓存。
func TestNewRedisCache_RetriesAfterCooldown(t *testing.T) {
	resetCacheState(t)

	calls := 0
	initRedisFunc = func(config.Redis) (Cache, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("redis down")
		}
		return &fakeCache{}, nil
	}

	now := time.Now()
	clockNow = func() time.Time { return now }

	if got := NewRedisCache(); got != nil {
		t.Fatal("第一次连接失败应返回 nil")
	}
	if calls != 1 {
		t.Fatalf("应只尝试一次，实际 %d", calls)
	}

	// 冷却期内：不能每个请求都去重连。
	now = now.Add(cacheRetryCooldown / 2)
	if got := NewRedisCache(); got != nil {
		t.Fatal("冷却期内应返回 nil")
	}
	if calls != 1 {
		t.Fatalf("冷却期内不应重连，实际尝试 %d 次", calls)
	}

	// 冷却结束：Redis 已恢复，应该重新连接并缓存实例。
	now = now.Add(cacheRetryCooldown)
	if got := NewRedisCache(); got == nil {
		t.Fatal("冷却结束后应重新连接成功")
	}
	if calls != 2 {
		t.Fatalf("冷却结束后应重试一次，实际 %d", calls)
	}
}

func TestNewRedisCache_SuccessIsCached(t *testing.T) {
	resetCacheState(t)

	calls := 0
	initRedisFunc = func(config.Redis) (Cache, error) {
		calls++
		return &fakeCache{}, nil
	}

	if got := NewRedisCache(); got == nil {
		t.Fatal("应连接成功")
	}
	if got := NewRedisCache(); got == nil {
		t.Fatal("第二次应返回已缓存实例")
	}
	if calls != 1 {
		t.Fatalf("成功后不应重复连接，实际 %d", calls)
	}
}

func TestNewRedisCache_ConcurrentCallsOnlyConnectOnce(t *testing.T) {
	resetCacheState(t)

	calls := 0
	initRedisFunc = func(config.Redis) (Cache, error) {
		calls++
		time.Sleep(5 * time.Millisecond)
		return &fakeCache{}, nil
	}

	done := make(chan struct{})
	for i := 0; i < 10; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			if NewRedisCache() == nil {
				t.Error("应连接成功")
			}
		}()
	}
	for i := 0; i < 10; i++ {
		<-done
	}
	if calls != 1 {
		t.Fatalf("并发首次调用应只连接一次，实际 %d", calls)
	}
}
