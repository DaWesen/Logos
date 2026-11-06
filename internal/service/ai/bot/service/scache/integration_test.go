package scache

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// bagOfRunes 是确定性的本地 embedder：每个字符落在一个固定维度上，
// 两个句子共享的字符越多，余弦相似度越高。测试不依赖任何外部 API。
func bagOfRunes(_ context.Context, text string) ([]float32, error) {
	const dim = 4096
	vec := make([]float32, dim)
	for _, r := range text {
		if r == ' ' {
			continue
		}
		vec[int(r)%dim]++
	}
	return vec, nil
}

func oneHot(dim, idx int) []float32 {
	vec := make([]float32, dim)
	vec[idx%dim] = 1
	return vec
}

type fakeClock struct {
	nanos atomic.Int64
}

func newFakeClock(at time.Time) *fakeClock {
	c := &fakeClock{}
	c.nanos.Store(at.UnixNano())
	return c
}

func (c *fakeClock) Now() time.Time {
	return time.Unix(0, c.nanos.Load())
}

func (c *fakeClock) Advance(d time.Duration) {
	c.nanos.Add(int64(d))
}

func waitFor(t *testing.T, timeout time.Duration, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", desc)
}

func TestIntegration_WriteThenRead(t *testing.T) {
	cfg := testConfig()
	c, _ := newCacheWithFakeRedis(t, cfg)
	scope := testScope("0")
	query := "退款政策是什么"
	answer := "七天无理由退款，到账时间以银行处理为准。"

	store(t, c, scope, query, answer, testVector(1))
	waitEntries(t, c, 1)

	result := c.Lookup(context.Background(), LookupRequest{
		Scope:  scope,
		Query:  query,
		Vector: testVector(1),
	})
	if result.Decision != HitExact {
		t.Fatalf("decision = %s, want %s", result.Decision, HitExact)
	}
	if result.Answer != answer {
		t.Fatalf("answer = %q, want %q", result.Answer, answer)
	}
}

func TestIntegration_ParaphraseHitsVector(t *testing.T) {
	cfg := testConfig()
	cfg.MidThreshold = 0.50
	cfg.HighThreshold = 0.60
	c := newTestCache(t, cfg)
	scope := testScope("0")
	answer := "在订单页提交退款申请，审核通过后原路退回。"

	c.Store(context.Background(), StoreRequest{
		Scope:  scope,
		Query:  "如何申请退款",
		Answer: answer,
		Embed:  bagOfRunes,
		Source: "chat",
	})
	waitEntries(t, c, 1)

	result := c.Lookup(context.Background(), LookupRequest{
		Scope: scope,
		Query: "退款怎么申请",
		Embed: bagOfRunes,
	})
	if result.Decision != HitVector {
		t.Fatalf("decision = %s, similarity = %.3f, want %s", result.Decision, result.Similarity, HitVector)
	}
	if result.Answer != answer {
		t.Fatalf("answer = %q, want %q", result.Answer, answer)
	}
}

func TestIntegration_TTLExpiry(t *testing.T) {
	cfg := testConfig()
	clock := newFakeClock(time.Unix(1_700_000_000, 0))
	cfg.Now = clock.Now
	cfg.EntryTTL = time.Minute
	c, _ := newCacheWithFakeRedis(t, cfg)
	scope := testScope("0")
	query := "缓存条目的过期时间怎么算"

	store(t, c, scope, query, "写入时间加 TTL，再加最多 10% 抖动。", testVector(1))
	waitEntries(t, c, 1)

	clock.Advance(2 * time.Minute)
	result := c.Lookup(context.Background(), LookupRequest{
		Scope:  scope,
		Query:  query,
		Vector: testVector(1),
	})
	if result.Decision == HitExact || result.Decision == HitVector {
		t.Fatalf("decision = %s after TTL, want miss", result.Decision)
	}
}

func TestIntegration_ConcurrentStore(t *testing.T) {
	cfg := testConfig()
	cfg.MaxEntriesPerScope = 256
	cfg.MaxTotalEntries = 256
	cfg.WriteQueueSize = 256
	cfg.WriteWorkers = 4
	c, _ := newCacheWithFakeRedis(t, cfg)
	scope := testScope("0")

	const n = 100
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			query := fmt.Sprintf("并发写入的问题编号 %03d", i)
			c.Store(context.Background(), StoreRequest{
				Scope:  scope,
				Query:  query,
				Answer: "并发写入的答案内容足够长，用于通过缓存准入检查。",
				Vector: oneHot(n, i),
				Source: "chat",
			})
		}(i)
	}
	wg.Wait()
	waitEntries(t, c, n)

	if entries, _ := c.Stats(); entries != n {
		t.Fatalf("entries = %d, want %d", entries, n)
	}
	for i := 0; i < n; i++ {
		query := fmt.Sprintf("并发写入的问题编号 %03d", i)
		if entry := c.idx.exact(scope.Hash(), Normalize(query), time.Now().Unix()); entry == nil {
			t.Fatalf("query %d was not stored", i)
		}
	}
}

func TestIntegration_Eviction(t *testing.T) {
	cfg := testConfig()
	cfg.MaxEntriesPerScope = 3
	clock := newFakeClock(time.Unix(1_700_000_000, 0))
	cfg.Now = clock.Now
	c := newTestCache(t, cfg)
	scope := testScope("0")

	queries := make([]string, 5)
	for i := range queries {
		queries[i] = fmt.Sprintf("容量淘汰问题编号 %d", i)
		store(t, c, scope, queries[i], "答案内容足够长，用于通过缓存准入检查。", oneHot(5, i))
		clock.Advance(time.Second)
	}

	waitFor(t, 2*time.Second, "newest entry", func() bool {
		return c.idx.exact(scope.Hash(), Normalize(queries[4]), clock.Now().Unix()) != nil
	})
	if entries, _ := c.Stats(); entries != 3 {
		t.Fatalf("entries = %d, want 3", entries)
	}
	for _, query := range queries[:2] {
		if entry := c.idx.exact(scope.Hash(), Normalize(query), clock.Now().Unix()); entry != nil {
			t.Fatalf("oldest entry %q was not evicted", query)
		}
	}
}

func TestIntegration_KBBumpInvalidates(t *testing.T) {
	cfg := testConfig()
	c, _ := newCacheWithFakeRedis(t, cfg)
	ctx := context.Background()
	query := "知识库更新后旧答案还能命中吗"

	scope := testScope(c.KBVersion(ctx, "bot-1"))
	store(t, c, scope, query, "不能，Scope 会带上新的 KBVersion。", testVector(1))
	waitEntries(t, c, 1)

	if got := c.BumpKBVersion(ctx, "bot-1"); got == 0 {
		t.Fatal("BumpKBVersion returned 0")
	}
	newScope := testScope(c.KBVersion(ctx, "bot-1"))
	if newScope.Hash() == scope.Hash() {
		t.Fatal("scope hash did not change after KB bump")
	}

	result := c.Lookup(ctx, LookupRequest{
		Scope:  newScope,
		Query:  query,
		Vector: testVector(1),
	})
	if result.Decision == HitExact || result.Decision == HitVector {
		t.Fatalf("decision = %s after KB bump, want miss", result.Decision)
	}
}

func TestPersistExactTTLUsesConfiguredValue(t *testing.T) {
	tests := []struct {
		name     string
		exactTTL time.Duration
		want     func(bucketTTL time.Duration) time.Duration
	}{
		{
			name:     "shorter than entry ttl",
			exactTTL: 10 * time.Minute,
			want: func(time.Duration) time.Duration {
				return 10 * time.Minute
			},
		},
		{
			name:     "longer than entry ttl is clamped",
			exactTTL: 48 * time.Hour,
			want: func(bucketTTL time.Duration) time.Duration {
				return bucketTTL
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := testConfig()
			cfg.EntryTTL = time.Hour
			cfg.ExactTTL = tt.exactTTL
			c, fake := newCacheWithFakeRedis(t, cfg)
			scope := testScope("0")

			store(t, c, scope, "精确层过期时间应该跟随配置", "精确层单独设置 TTL，且不能超过条目有效期。", testVector(1))

			var bucketTTL, exactTTL time.Duration
			waitFor(t, 2*time.Second, "persist ttl", func() bool {
				bucketTTL = fake.ttlOf(bucketKey(scope.Hash()))
				exactTTL = fake.ttlOf(exactKey(scope.Hash()))
				return bucketTTL > 0 && exactTTL > 0
			})

			if exactTTL != tt.want(bucketTTL) {
				t.Fatalf("exact ttl = %s, want %s (bucket ttl %s)", exactTTL, tt.want(bucketTTL), bucketTTL)
			}
			if exactTTL > bucketTTL {
				t.Fatalf("exact ttl %s exceeds entry ttl %s", exactTTL, bucketTTL)
			}
		})
	}
}
