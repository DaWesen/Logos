package qqbridge

import (
	"context"
	"testing"
	"time"

	"Logos/config"
	"Logos/pkg/cache"
)

// memCache 是只实现 Mapper 用到方法的最小内存缓存。
type memCache struct {
	cache.Cache
	values map[string]string
}

func newMemCache() *memCache { return &memCache{values: map[string]string{}} }

func (c *memCache) Exists(_ context.Context, key string) (bool, error) {
	_, ok := c.values[key]
	return ok, nil
}
func (c *memCache) Get(_ context.Context, key string) (string, error) { return c.values[key], nil }
func (c *memCache) Set(_ context.Context, key string, value interface{}, _ time.Duration) error {
	c.values[key] = value.(string)
	return nil
}
func (c *memCache) HMSet(_ context.Context, key string, _ map[string]interface{}) error {
	c.values[key] = "1"
	return nil
}
func (c *memCache) Expire(context.Context, string, time.Duration) error { return nil }

// TestMapper_RecoversWhenRedisComesBack
// 构造时 Redis 不可用（工厂返回 nil），之后恢复：Mapper 必须能拿到新实例，
// 而不是像缓存字段那样永久为 nil。
func TestMapper_RecoversWhenRedisComesBack(t *testing.T) {
	m := NewMapper(&config.Config{})
	m.redis = nil

	available := false
	mem := newMemCache()
	m.redisFactory = func() cache.Cache {
		if !available {
			return nil
		}
		return mem
	}

	// Redis 不可用：仍返回确定性 ID，不报错。
	got, err := m.GetOrCreateUser(context.Background(), 10001, "alice")
	if err != nil {
		t.Fatalf("缓存不可用时不应报错: %v", err)
	}
	if got != "qq_10001" {
		t.Fatalf("应返回确定性 ID，实际 %q", got)
	}
	if len(mem.values) != 0 {
		t.Fatal("缓存不可用时不应写入")
	}

	// Redis 恢复：后续调用应真正写入缓存。
	available = true
	if _, err := m.GetOrCreateUser(context.Background(), 10001, "alice"); err != nil {
		t.Fatalf("恢复后不应报错: %v", err)
	}
	if len(mem.values) == 0 {
		t.Fatal("Redis 恢复后 Mapper 仍拿不到实例，映射没有写入")
	}
}

func TestMapper_GroupFallsBackWithoutCache(t *testing.T) {
	m := NewMapper(&config.Config{})
	m.redisFactory = func() cache.Cache { return nil }

	got, err := m.GetOrCreateGroup(context.Background(), 20002, "group")
	if err != nil {
		t.Fatalf("缓存不可用时不应报错: %v", err)
	}
	if got != "qqgroup_20002" {
		t.Fatalf("应返回确定性群 ID，实际 %q", got)
	}
}

func TestMapper_ReverseLookupRequiresCache(t *testing.T) {
	m := NewMapper(&config.Config{})
	m.redisFactory = func() cache.Cache { return nil }

	if _, err := m.GetQQNumberByLogosID(context.Background(), "qq_10001"); err == nil {
		t.Fatal("反查依赖缓存，缓存不可用时应返回错误而不是空字符串")
	}
}
