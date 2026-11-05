package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"Logos/internal/service/ai/recommend/model"
	"Logos/pkg/cache"
	"Logos/pkg/singleflight"
)

// faultyRecommendCache 模拟缓存读故障与脏值，并记录写回/删除次数。
type faultyRecommendCache struct {
	cache.Cache
	mu      sync.Mutex
	data    string
	getErr  error
	sets    int
	deletes int
}

func (f *faultyRecommendCache) Get(context.Context, string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return "", f.getErr
	}
	return f.data, nil
}

func (f *faultyRecommendCache) Set(_ context.Context, _ string, value interface{}, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sets++
	f.data = value.(string)
	return nil
}

func (f *faultyRecommendCache) Delete(context.Context, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deletes++
	f.data = ""
	return nil
}

// TestGetRecommendations_CacheReadErrorFallsBack 验证读缓存失败时仍能回源，
// 且结果会重新写回缓存，而不是因为一次读失败就放弃缓存。
func TestGetRecommendations_CacheReadErrorFallsBack(t *testing.T) {
	c := &faultyRecommendCache{getErr: errors.New("redis down")}
	repo := &fakeRecommendRepo{items: []*model.RecommendationItem{{ID: "a"}}}
	svc := &recommendServiceImpl{
		recommendRepo: repo,
		cache:         c,
		fills:         singleflight.NewGroup[recommendResult](time.Second),
	}

	items, total, err := svc.GetRecommendations(context.Background(), 1, "", 10, nil)
	if err != nil {
		t.Fatalf("读缓存失败不应让推荐失败: %v", err)
	}
	if len(items) != 1 || total != 1 {
		t.Fatalf("应回源拿到 1 条推荐，实际 items=%d total=%d", len(items), total)
	}
	if got := atomic.LoadInt32(&repo.calls); got != 1 {
		t.Fatalf("应回源 1 次，实际 %d 次", got)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sets != 1 {
		t.Fatalf("回源结果应写回缓存 1 次，实际 %d 次", c.sets)
	}
}

// TestGetRecommendations_CorruptCacheDeletedAndRefetched 验证脏缓存会被删除，
// 否则 TTL 内每个请求都会重复解析失败、反复回源。
func TestGetRecommendations_CorruptCacheDeletedAndRefetched(t *testing.T) {
	c := &faultyRecommendCache{data: "{not-json"}
	repo := &fakeRecommendRepo{items: []*model.RecommendationItem{{ID: "a"}}}
	svc := &recommendServiceImpl{
		recommendRepo: repo,
		cache:         c,
		fills:         singleflight.NewGroup[recommendResult](time.Second),
	}

	items, _, err := svc.GetRecommendations(context.Background(), 1, "", 10, nil)
	if err != nil {
		t.Fatalf("脏缓存应按未命中回源而不是报错: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("应回源拿到 1 条推荐，实际 %d 条", len(items))
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.deletes != 1 {
		t.Fatalf("损坏缓存应删除 1 次，实际 %d 次", c.deletes)
	}
	if c.sets != 1 {
		t.Fatalf("回源后应写入新缓存 1 次，实际 %d 次", c.sets)
	}
}
