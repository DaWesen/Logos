package qqbridge

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"Logos/config"
)

type fakeBindingCache struct {
	mu     sync.Mutex
	values map[string]string
	failOn string
}

func (c *fakeBindingCache) Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failOn == key {
		return errors.New("redis unavailable")
	}
	if c.values == nil {
		c.values = make(map[string]string)
	}
	c.values[key] = value.(string)
	return nil
}

func TestWarmupBotBindings_WritesAllBindings(t *testing.T) {
	cache := &fakeBindingCache{}
	written, err := WarmupBotBindings(context.Background(), []BotBinding{
		{QQNumber: "10001", BotID: "bot-a"},
		{QQNumber: "10002", BotID: "bot-b"},
	}, cache)
	if err != nil {
		t.Fatalf("预热失败: %v", err)
	}
	if written != 2 {
		t.Fatalf("written = %d, want 2", written)
	}
	if cache.values["qq:bot_bind:10001"] != "bot-a" ||
		cache.values["qq:bot_bind:10002"] != "bot-b" {
		t.Fatalf("绑定缓存内容不对: %v", cache.values)
	}
}

func TestWarmupBotBindings_PartialFailureKeepsGoing(t *testing.T) {
	cache := &fakeBindingCache{failOn: "qq:bot_bind:10001"}
	written, err := WarmupBotBindings(context.Background(), []BotBinding{
		{QQNumber: "10001", BotID: "bot-a"},
		{QQNumber: "10002", BotID: "bot-b"},
	}, cache)
	if err == nil {
		t.Fatal("期望返回部分失败")
	}
	if written != 1 {
		t.Fatalf("written = %d, want 1", written)
	}
	if cache.values["qq:bot_bind:10002"] != "bot-b" {
		t.Fatal("单个 key 失败不应中断其余绑定")
	}
}

func TestWarmupBotBindings_SkipsIncompleteBindings(t *testing.T) {
	cache := &fakeBindingCache{}
	written, err := WarmupBotBindings(context.Background(), []BotBinding{
		{QQNumber: "", BotID: "bot-a"},
		{QQNumber: "10002", BotID: ""},
		{QQNumber: "10003", BotID: "bot-c"},
	}, cache)
	if err != nil {
		t.Fatalf("预热失败: %v", err)
	}
	if written != 1 {
		t.Fatalf("written = %d, want 1", written)
	}
	if len(cache.values) != 1 {
		t.Fatalf("不完整绑定不应写入: %v", cache.values)
	}
}

func TestWarmupBotBindings_NilCacheIsError(t *testing.T) {
	if _, err := WarmupBotBindings(context.Background(), []BotBinding{{QQNumber: "1", BotID: "b"}}, nil); err == nil {
		t.Fatal("缓存为 nil 时应返回错误")
	}
}

type fakeBindingLookup struct {
	botID string
	err   error
	calls int
}

func (l *fakeBindingLookup) LookupBotByQQ(ctx context.Context, qqNumber string) (string, error) {
	l.calls++
	return l.botID, l.err
}

func TestMessageConverter_FallsBackToDatabaseWhenCacheMiss(t *testing.T) {
	lookup := &fakeBindingLookup{botID: "bot-from-db"}
	converter := NewMessageConverter(&config.Config{})
	converter.SetBindingLookup(lookup)
	var setKey, setValue string
	converter.setCache(
		func(context.Context, string) (string, error) { return "", nil },
		func(_ context.Context, key, value string) error {
			setKey, setValue = key, value
			return nil
		},
	)

	got := converter.findBotByQQNumber(10001)
	if got != "bot-from-db" {
		t.Fatalf("回源结果 = %q, want bot-from-db", got)
	}
	if lookup.calls != 1 {
		t.Fatalf("回源调用次数 = %d, want 1", lookup.calls)
	}
	if setKey != "qq:bot_bind:10001" || setValue != "bot-from-db" {
		t.Fatalf("回填缓存不正确: key=%q value=%q", setKey, setValue)
	}
}

func TestMessageConverter_NoLookupReturnsEmpty(t *testing.T) {
	converter := NewMessageConverter(&config.Config{})
	converter.setCache(
		func(context.Context, string) (string, error) { return "", nil },
		func(context.Context, string, string) error { return nil },
	)
	if got := converter.findBotByQQNumber(10001); got != "" {
		t.Fatalf("未注入回源时不应返回 Bot ID: %q", got)
	}
}

func TestMessageConverter_CacheErrorFallsBackToDatabase(t *testing.T) {
	lookup := &fakeBindingLookup{botID: "bot-from-db"}
	converter := NewMessageConverter(&config.Config{})
	converter.SetBindingLookup(lookup)
	converter.setCache(
		func(context.Context, string) (string, error) { return "", errors.New("redis down") },
		func(context.Context, string, string) error { return errors.New("redis down") },
	)

	if got := converter.findBotByQQNumber(10001); got != "bot-from-db" {
		t.Fatalf("缓存故障时应回源，got %q", got)
	}
}

func TestMessageConverter_CacheHitSkipsDatabase(t *testing.T) {
	lookup := &fakeBindingLookup{botID: "bot-from-db"}
	converter := NewMessageConverter(&config.Config{})
	converter.SetBindingLookup(lookup)
	converter.setCache(
		func(context.Context, string) (string, error) { return "bot-from-cache", nil },
		func(context.Context, string, string) error { return nil },
	)

	if got := converter.findBotByQQNumber(10001); got != "bot-from-cache" {
		t.Fatalf("缓存命中时不应回源，got %q", got)
	}
	if lookup.calls != 0 {
		t.Fatalf("缓存命中不应查库，calls=%d", lookup.calls)
	}
}
