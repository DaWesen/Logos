package scache

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testConfig() Config {
	cfg := DefaultConfig()
	cfg.Mode = ModeOn
	cfg.CalibratedFor = "test/8"
	cfg.MidThreshold = 0.80
	cfg.HighThreshold = 0.95
	cfg.MinAnswerRunes = 1
	cfg.MaxAnswerRunes = 10000
	cfg.WriteQueueSize = 64
	cfg.WriteWorkers = 1
	cfg.EntryTTL = time.Hour
	cfg.ExactTTL = time.Hour
	cfg.EvictInterval = time.Hour
	cfg.ReplayBudget = 0
	cfg.ReplayMaxDelay = 0
	cfg.JudgeEnabled = false
	cfg.CacheProvider = nil
	return cfg
}

func newTestCache(t *testing.T, cfg Config) *Cache {
	t.Helper()
	c, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(c.Close)
	return c
}

func testScope(version string) Scope {
	return Scope{
		Tenant:     "default",
		BotID:      "bot-1",
		UserID:     "user-1",
		KBVersion:  version,
		PromptHash: PromptHash("system prompt"),
		ModelKey:   "openai/gpt/t=0",
		EmbedKey:   "embed/8",
	}
}

// waitEntries 等待后台写入 worker 把条目放进本地索引。
// 写入是异步的，测试不能假设 Store 返回时条目已经可见。
func waitEntries(t *testing.T, c *Cache, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if n, _ := c.Stats(); n >= want {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	n, _ := c.Stats()
	t.Fatalf("entries = %d, want >= %d", n, want)
}

func store(t *testing.T, c *Cache, scope Scope, query, answer string, vec []float32) {
	t.Helper()
	c.Store(context.Background(), StoreRequest{
		Scope:  scope,
		Query:  query,
		Answer: answer,
		Vector: vec,
		Source: "chat",
	})
}

func TestNormalize(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"  ＡＢＣ　１２３？ ", "abc 123"},
		{"你好，   世界。", "你好, 世界"},
		{"a\r\nb", "a b"},
	}
	for _, tt := range tests {
		if got := Normalize(tt.in); got != tt.want {
			t.Errorf("Normalize(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestScopeHashSeparatesDimensions(t *testing.T) {
	base := testScope("0")
	if base.Hash() == "" {
		t.Fatal("empty hash")
	}

	otherUser := base
	otherUser.UserID = "user-2"
	if base.Hash() == otherUser.Hash() {
		t.Fatal("scope hash must change with user id")
	}

	// 分隔符测试：字段边界不同但不能拼出同一个 key。
	a := base
	a.Tenant, a.BotID = "ab", "c"
	b := base
	b.Tenant, b.BotID = "a", "bc"
	if a.Hash() == b.Hash() {
		t.Fatal("scope hash must be field-boundary safe")
	}
}

func TestPromptHashNormalizesLineEndings(t *testing.T) {
	if PromptHash("a\r\nb") != PromptHash("a\nb") {
		t.Fatal("PromptHash should normalize CRLF")
	}
	if PromptHash("  same  ") != PromptHash("same") {
		t.Fatal("PromptHash should trim whitespace")
	}
	if PromptHash("same") == PromptHash("same but different") {
		t.Fatal("prompt hash collision")
	}
}

func TestSlotsGateNegationNumbersEntitiesAndRoute(t *testing.T) {
	if !Extract("这个方案不支持退款").Negated {
		t.Fatal("negation not detected")
	}
	if (SlotSet{Negated: true}).Compatible(SlotSet{}) {
		t.Fatal("negation mismatch must be rejected")
	}

	one := Extract("预算100元能买什么")
	two := Extract("预算200元能买什么")
	if one.Compatible(two) {
		t.Fatal("different numbers must be rejected")
	}

	ab := Extract("「北京」和「上海」哪个好")
	ba := Extract("「上海」和「北京」哪个好")
	if !ab.Compatible(ba) {
		t.Fatal("entity order must not affect compatibility")
	}
	if ab.Compatible(Extract("「广州」和「深圳」哪个好")) {
		t.Fatal("different entities must be rejected")
	}

	routeAB := Extract("从北京到上海怎么走")
	routeBA := Extract("从上海到北京怎么走")
	if routeAB.Route == "" || routeBA.Route == "" {
		t.Fatalf("route not extracted: %q %q", routeAB.Route, routeBA.Route)
	}
	if routeAB.Compatible(routeBA) {
		t.Fatal("opposite route direction must be rejected")
	}

	// 抽取器必然有漏抽，一方为空时必须放行，否则会把正常改写误杀成 miss。
	if !(SlotSet{}).Compatible(ab) {
		t.Fatal("missing slots must fail open")
	}

	// 数字槽位是刻意更严格的：一方带数字、另一方没有，也必须否决。
	if (SlotSet{}).Compatible(one) {
		t.Fatal("missing numbers must be rejected")
	}
}

func TestExactHitDoesNotCallEmbedding(t *testing.T) {
	c := newTestCache(t, testConfig())
	scope := testScope("0")
	query := "如何配置语义缓存的高阈值参数"
	store(t, c, scope, query, "先设置 high_threshold，再观察 shadow 判定分布。", []float32{1, 0})
	waitEntries(t, c, 1)

	var calls atomic.Int32
	result := c.Lookup(context.Background(), LookupRequest{
		Scope: scope,
		Query: " " + query + "？ ",
		Embed: func(context.Context, string) ([]float32, error) {
			calls.Add(1)
			return nil, errors.New("must not be called")
		},
	})
	if result.Decision != HitExact {
		t.Fatalf("decision = %s, want %s", result.Decision, HitExact)
	}
	if calls.Load() != 0 {
		t.Fatalf("embedding called %d times on exact hit", calls.Load())
	}
}

func TestEmbeddingFailureFallsBackToMiss(t *testing.T) {
	c := newTestCache(t, testConfig())
	scope := testScope("0")

	result := c.Lookup(context.Background(), LookupRequest{
		Scope: scope,
		Query: "一个足够长但从未缓存过的问题",
		Embed: func(context.Context, string) ([]float32, error) {
			return nil, errors.New("provider down")
		},
	})
	if result.Decision != MissError {
		t.Fatalf("decision = %s, want %s", result.Decision, MissError)
	}
}

func TestShadowModeNeverReturnsAnswer(t *testing.T) {
	cfg := testConfig()
	cfg.Mode = ModeShadow
	c := newTestCache(t, cfg)
	scope := testScope("0")

	store(t, c, scope, "北京周末适合去哪里玩", "北京周末可以去故宫和颐和园。", []float32{1, 0})
	waitEntries(t, c, 1)

	result := c.Lookup(context.Background(), LookupRequest{
		Scope:  scope,
		Query:  "上海周末适合去哪里玩",
		Vector: []float32{1, 0},
	})
	if result.Decision != MissShadowed {
		t.Fatalf("decision = %s, want %s", result.Decision, MissShadowed)
	}
	if result.Answer != "" {
		t.Fatalf("shadow mode returned answer %q", result.Answer)
	}
	if result.ShadowDecision != HitVector {
		t.Fatalf("shadow decision = %s, want %s", result.ShadowDecision, HitVector)
	}
}

func TestSlotCompatibleCandidateWinsOverHigherSimilarityConflict(t *testing.T) {
	c := newTestCache(t, testConfig())
	scope := testScope("0")

	// 100 元这条相似度最高，但数字槽位与查询冲突，不能返回。
	store(t, c, scope, "帮我算下预算100元能买什么", "一百元的答案", []float32{1, 0})
	// 200 元这条相似度略低，但槽位完全兼容，才是正确命中。
	store(t, c, scope, "帮我算下预算200元能买什么", "两百元的答案", []float32{0.99, 0.141})
	waitEntries(t, c, 2)

	result := c.Lookup(context.Background(), LookupRequest{
		Scope:  scope,
		Query:  "预算200元能买什么",
		Vector: []float32{1, 0},
	})
	if !result.Decision.Hit() {
		t.Fatalf("decision = %s, want hit", result.Decision)
	}
	if result.Answer != "两百元的答案" {
		t.Fatalf("answer = %q, want the slot-compatible entry", result.Answer)
	}
}

func TestGrayZoneWithoutJudgeIsMiss(t *testing.T) {
	c := newTestCache(t, testConfig())
	scope := testScope("0")

	store(t, c, scope, "缓存命中率怎么统计", "按 hit 与 lookup 的比值统计。", []float32{1, 0})
	waitEntries(t, c, 1)

	// 0.9 落在 [mid, high)，判官未启用时必须保守返回 miss。
	result := c.Lookup(context.Background(), LookupRequest{
		Scope:  scope,
		Query:  "语义缓存命中率如何计算",
		Vector: []float32{0.9, 0.4359},
	})
	if result.Decision != MissJudge {
		t.Fatalf("decision = %s, want %s", result.Decision, MissJudge)
	}
}

func TestStoreAdmissionRejectsUnsafeTurns(t *testing.T) {
	tests := []struct {
		name string
		req  StoreRequest
	}{
		{"planner", StoreRequest{Source: "planner"}},
		{"tool", StoreRequest{Source: "chat", ToolCalled: true}},
		{"injection", StoreRequest{Source: "chat", InjectionGuardHit: true}},
		{"temperature", StoreRequest{Source: "chat", Temperature: 0.7}},
		{"degraded", StoreRequest{Source: "chat", Answer: "抱歉，我暂时无法回答这个问题，请稍后再试。"}},
		{"time_sensitive", StoreRequest{Source: "chat", Query: "今天上海天气怎么样"}},
		{"too_short", StoreRequest{Source: "chat", Answer: "短"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := testConfig()
			cfg.MinAnswerRunes = 10
			c := newTestCache(t, cfg)

			req := tt.req
			req.Scope = testScope("0")
			if req.Query == "" {
				req.Query = "一个足够长的普通问题用于准入测试"
			}
			if req.Answer == "" {
				req.Answer = "这是一个长度足够且不含兜底文案的正常答案。"
			}
			req.Vector = []float32{1, 0}

			c.Store(context.Background(), req)
			time.Sleep(20 * time.Millisecond)
			if n, _ := c.Stats(); n != 0 {
				t.Fatalf("unsafe turn was stored: %d entries", n)
			}
		})
	}
}

func TestTTLExpiry(t *testing.T) {
	cfg := testConfig()
	now := time.Unix(1_700_000_000, 0)
	cfg.Now = func() time.Time { return now }
	cfg.EntryTTL = time.Minute
	c := newTestCache(t, cfg)
	scope := testScope("0")

	store(t, c, scope, "缓存条目的过期时间怎么算", "写入时间加 TTL 再加最多 10% 抖动。", []float32{1, 0})
	waitEntries(t, c, 1)

	now = now.Add(2 * time.Minute)
	result := c.Lookup(context.Background(), LookupRequest{
		Scope:  scope,
		Query:  "缓存条目的过期时间怎么算",
		Vector: []float32{1, 0},
	})
	if result.Decision != MissLowSim {
		t.Fatalf("decision = %s, want %s after TTL", result.Decision, MissLowSim)
	}
}

func TestBumpKBVersionInvalidatesOldScope(t *testing.T) {
	cfg := testConfig()
	c := newTestCache(t, cfg)
	ctx := context.Background()

	version := c.KBVersion(ctx, "bot-1")
	scope := testScope(version)
	store(t, c, scope, "知识库更新后旧答案还能命中吗", "不能，Scope 会带上新的 KBVersion。", []float32{1, 0})
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
		Query:  "知识库更新后旧答案还能命中吗",
		Vector: []float32{1, 0},
	})
	if result.Decision != MissLowSim {
		t.Fatalf("decision = %s, want %s after KB bump", result.Decision, MissLowSim)
	}
}

func TestReplaySplitAndStream(t *testing.T) {
	answer := "第一句。第二句。第三句。"
	chunks := SplitForReplay(answer, 4)
	if len(chunks) != 3 {
		t.Fatalf("chunks = %d, want 3 (%q)", len(chunks), chunks)
	}
	if strings.Join(chunks, "") != answer {
		t.Fatalf("replay lost content: %q", strings.Join(chunks, ""))
	}

	var got strings.Builder
	if err := ReplayAsStream(answer, 4, 0, 0, func(chunk string) error {
		got.WriteString(chunk)
		return nil
	}); err != nil {
		t.Fatalf("ReplayAsStream error = %v", err)
	}
	if got.String() != answer {
		t.Fatalf("streamed = %q, want %q", got.String(), answer)
	}

	stop := errors.New("client gone")
	if err := ReplayAsStream(answer, 4, 0, 0, func(string) error { return stop }); !errors.Is(err, stop) {
		t.Fatalf("error = %v, want %v", err, stop)
	}
}

func TestConcurrentLookupStoreAndBump(t *testing.T) {
	c := newTestCache(t, testConfig())
	ctx := context.Background()
	var wg sync.WaitGroup

	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			scope := testScope("0")
			for j := 0; j < 50; j++ {
				c.Store(ctx, StoreRequest{
					Scope:  scope,
					Query:  "并发写入的普通问题用于竞态检测",
					Answer: "这是一个用于并发测试的正常答案，长度足够。",
					Vector: []float32{1, 0},
					Source: "chat",
				})
				c.Lookup(ctx, LookupRequest{
					Scope:  scope,
					Query:  "并发读取的普通问题用于竞态检测",
					Vector: []float32{1, 0},
				})
				if i == 0 {
					c.BumpKBVersion(ctx, "bot-1")
				}
				c.Stats()
			}
		}(i)
	}
	wg.Wait()
}

func TestCloseIsIdempotent(t *testing.T) {
	c, err := New(testConfig())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	c.Close()
	c.Close()
}
