package scache

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"Logos/pkg/cache"
)

func newCacheWithFakeRedis(t *testing.T, cfg Config) (*Cache, *fakeRedis) {
	t.Helper()
	fake := newFakeRedis()
	cfg.CacheProvider = func() cache.Cache { return fake }
	c := newTestCache(t, cfg)
	if c.PrimeRedis() == nil {
		t.Fatal("PrimeRedis() = nil, want fake redis")
	}
	return c, fake
}

func testVector(v float32) []float32 {
	return []float32{v, 0, 0, 0, 0, 0, 0, 0}
}

// 重启后精确命中不应再付 embedding：exact Hash 是冷启动的最短路径。
func TestLookup_ExactHitFromRedisSkipsEmbedAfterRestart(t *testing.T) {
	cfg := testConfig()
	scope := testScope("0")
	query := "退款政策是什么"
	answer := "七天无理由退款，到账时间以银行处理为准。"

	first, fake := newCacheWithFakeRedis(t, cfg)
	store(t, first, scope, query, answer, testVector(1))
	waitEntries(t, first, 1)
	first.Close()

	field := NormalizeHash(Normalize(query))
	if id := fake.hashes[exactKey(scope.Hash())][field]; id == "" {
		t.Fatal("exact hash was not persisted")
	}

	var embeds int32
	cfg.CacheProvider = func() cache.Cache { return fake }
	second, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(second.Close)
	if second.PrimeRedis() == nil {
		t.Fatal("PrimeRedis() = nil, want fake redis")
	}

	result := second.Lookup(context.Background(), LookupRequest{
		Scope: scope,
		Query: query,
		Embed: func(context.Context, string) ([]float32, error) {
			atomic.AddInt32(&embeds, 1)
			return testVector(1), nil
		},
	})
	if result.Decision != HitExact {
		t.Fatalf("decision = %s, want %s", result.Decision, HitExact)
	}
	if result.Answer != answer {
		t.Fatalf("answer = %q, want %q", result.Answer, answer)
	}
	if got := atomic.LoadInt32(&embeds); got != 0 {
		t.Fatalf("embed calls = %d, want 0", got)
	}
}

func TestForgetUserRemovesAllScopes(t *testing.T) {
	cfg := testConfig()
	c, fake := newCacheWithFakeRedis(t, cfg)

	scopeA := testScope("0")
	scopeB := scopeA
	scopeB.BotID = "bot-2"
	store(t, c, scopeA, "退款政策是什么", "第一个答案，长度足够用于缓存准入。", testVector(1))
	store(t, c, scopeB, "退货流程是什么", "第二个答案，长度足够用于缓存准入。", testVector(0.9))
	waitEntries(t, c, 2)

	deleted, err := c.ForgetUser(context.Background(), scopeA.UserID)
	if err != nil {
		t.Fatalf("ForgetUser() error = %v", err)
	}
	if deleted != 2 {
		t.Fatalf("deleted = %d, want 2", deleted)
	}
	if entries, _ := c.Stats(); entries != 0 {
		t.Fatalf("local entries = %d, want 0", entries)
	}
	if members := fake.sets[userKey(scopeA.UserID)]; len(members) != 0 {
		t.Fatalf("user index size = %d, want 0", len(members))
	}
	if _, ok := fake.hashes[bucketKey(scopeA.Hash())]; ok {
		t.Fatal("bucket A still exists after ForgetUser")
	}
	if _, ok := fake.hashes[bucketKey(scopeB.Hash())]; ok {
		t.Fatal("bucket B still exists after ForgetUser")
	}
}

func TestStoreRejectsPersonalData(t *testing.T) {
	c := newTestCache(t, testConfig())
	store(t, c, testScope("0"), "我的订单余额是多少", "这里是足够长的答案内容，用于通过长度检查。", testVector(1))
	if entries, _ := c.Stats(); entries != 0 {
		t.Fatalf("entries = %d, want 0 for personal data", entries)
	}
}

func TestStorePriceQuestionUsesShortTTL(t *testing.T) {
	cfg := testConfig()
	c := newTestCache(t, cfg)
	scope := testScope("0")
	query := "这个产品多少钱"
	store(t, c, scope, query, "这个产品售价 199 元，活动期间可能有优惠。", testVector(1))
	waitEntries(t, c, 1)

	entry := c.idx.exact(scope.Hash(), Normalize(query), time.Now().Unix())
	if entry == nil {
		t.Fatal("entry not found")
	}
	ttl := time.Duration(entry.ExpireAt-entry.CreatedAt) * time.Second
	if ttl < cfg.PriceTTL || ttl > cfg.PriceTTL+cfg.PriceTTL/10+time.Second {
		t.Fatalf("ttl = %s, want within [%s, %s]", ttl, cfg.PriceTTL, cfg.PriceTTL+cfg.PriceTTL/10)
	}
}

func TestLookupMergesConcurrentEmbedding(t *testing.T) {
	cfg := testConfig()
	c := newTestCache(t, cfg)
	scope := testScope("0")

	var embeds int32
	embed := func(context.Context, string) ([]float32, error) {
		atomic.AddInt32(&embeds, 1)
		time.Sleep(30 * time.Millisecond)
		return testVector(1), nil
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.Lookup(context.Background(), LookupRequest{Scope: scope, Query: "同一个问题", Embed: embed})
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt32(&embeds); got != 1 {
		t.Fatalf("embed calls = %d, want 1", got)
	}
}
