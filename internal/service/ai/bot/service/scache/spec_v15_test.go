package scache

import (
	"context"
	"testing"
	"time"
)

// 本文件按 docs/semantic-cache-v2-spec.md §15.1 的用例名组织。
// 已有等价覆盖（如 TestBumpKBVersionInvalidatesOldScope）仍保留；
// 这里补的是「规格点 → 可执行证据」的显式对应，方便 review 时逐条勾选。

// TestScope_DifferentKBVersionMisses 验证 D1：知识库版本是复用边界，
// 同一用户、同一句话在 KB 更新后必须物理不可达。
func TestScope_DifferentKBVersionMisses(t *testing.T) {
	c := newTestCache(t, testConfig())
	query := "知识库版本变化后这句话还能命中吗"
	vec := []float32{1, 0}

	store(t, c, testScope("kb-1"), query, "版本更新后旧答案必须失效。", vec)
	waitEntries(t, c, 1)

	result := c.Lookup(context.Background(), LookupRequest{
		Scope:  testScope("kb-2"),
		Query:  query,
		Vector: vec,
	})
	if result.Decision != MissLowSim {
		t.Fatalf("decision = %s, want %s across KB versions", result.Decision, MissLowSim)
	}
}

// TestScope_DifferentPromptHashMisses 验证 D1：系统提示词一变，
// 旧答案的语气、约束和工具说明都可能不再成立。
func TestScope_DifferentPromptHashMisses(t *testing.T) {
	c := newTestCache(t, testConfig())
	query := "系统提示词变化后这句话还能命中吗"
	vec := []float32{1, 0}

	oldScope := testScope("0")
	store(t, c, oldScope, query, "提示词变化后旧答案必须失效。", vec)
	waitEntries(t, c, 1)

	newScope := oldScope
	newScope.PromptHash = PromptHash("a completely different system prompt")
	result := c.Lookup(context.Background(), LookupRequest{
		Scope:  newScope,
		Query:  query,
		Vector: vec,
	})
	if result.Decision != MissLowSim {
		t.Fatalf("decision = %s, want %s across prompt hashes", result.Decision, MissLowSim)
	}
}

// TestSlot_EntityOrderInsensitive 对应规格里的「注意」：
// 实体集合相同不代表问题相同。「北京到上海」与「上海到北京」实体一致，
// 但方向相反，必须靠 Route 槽位否决；而普通的并列实体顺序不影响复用。
func TestSlot_EntityOrderInsensitive(t *testing.T) {
	routeAB := Extract("从北京到上海怎么走")
	routeBA := Extract("从上海到北京怎么走")
	if routeAB.Route == "" || routeBA.Route == "" {
		t.Fatalf("route not extracted: %q %q", routeAB.Route, routeBA.Route)
	}
	if routeAB.Compatible(routeBA) {
		t.Fatal("opposite route direction must miss")
	}

	// 对照组：没有方向语义时，实体顺序不同仍应可复用。
	ab := Extract("「北京」和「上海」哪个更适合定居")
	ba := Extract("「上海」和「北京」哪个更适合定居")
	if !ab.Compatible(ba) {
		t.Fatal("plain entity order must stay compatible")
	}
}

// TestSlot_MissingExtractionPasses 验证「一方实体为空则放行」：
// 抽取器漏抽是常态，硬否决会把正常改写误杀成 miss；漏抽时回退到
// 向量 + 判官，方向仍然是安全的。
func TestSlot_MissingExtractionPasses(t *testing.T) {
	withEntity := Extract("「北京」和「上海」哪个更适合定居")
	empty := SlotSet{}
	if withEntity.Empty() {
		t.Fatal("entity slots should not be empty")
	}
	if !empty.Compatible(withEntity) {
		t.Fatal("empty left side must fail open")
	}
	if !withEntity.Compatible(empty) {
		t.Fatal("empty right side must fail open")
	}
}

// TestEntry_ValidRejectsWrongDim 验证 D5：维度不符的条目必须丢弃，
// 不能拿另一个 embedding 模型产生的向量参与相似度计算。
func TestEntry_ValidRejectsWrongDim(t *testing.T) {
	scopeHash := testScope("0").Hash()
	base := &Entry{
		ID:        "entry-1",
		Query:     "一个正常的问题",
		NormQuery: "一个正常的问题",
		Answer:    "一个正常的答案",
		Vec:       []float32{1, 0, 0},
		Dim:       3,
		ScopeHash: scopeHash,
	}
	if !base.Valid(scopeHash, 3) {
		t.Fatal("baseline entry must be valid")
	}
	if base.Valid(scopeHash, 8) {
		t.Fatal("entry with different expected dim must be rejected")
	}

	truncated := *base
	truncated.Vec = []float32{1, 0}
	if truncated.Valid(scopeHash, 0) {
		t.Fatal("entry whose Vec length disagrees with Dim must be rejected")
	}

	foreign := *base
	foreign.ScopeHash = "another-scope"
	if foreign.Valid(scopeHash, 3) {
		t.Fatal("entry from another scope must be rejected")
	}
}

// TestStore_QueueFullDrops 验证 C2/G8：写队列满时 Store 必须立刻丢弃，
// 不允许阻塞主链路，也不允许 panic。
//
// 这里刻意构造一个没有 worker 的 Cache 并预先占满队列：如果 Store 走的是
// 阻塞发送，测试会在超时分支失败；如果是 default 丢弃，原始哨兵任务仍在队列里。
func TestStore_QueueFullDrops(t *testing.T) {
	cfg := testConfig()
	c := &Cache{
		cfg:     cfg,
		writeCh: make(chan writeJob, 1),
		stopCh:  make(chan struct{}),
	}
	c.writeCh <- writeJob{}

	done := make(chan struct{})
	go func() {
		defer close(done)
		c.Store(context.Background(), StoreRequest{
			Mode:   ModeOn,
			Scope:  testScope("0"),
			Query:  "队列满时这条回答应该被丢弃",
			Answer: "队列满不能阻塞主链路，直接丢弃这一次写入。",
			Vector: []float32{1, 0},
			Source: "chat",
		})
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Store blocked when write queue was full")
	}

	if got := len(c.writeCh); got != 1 {
		t.Fatalf("queue length = %d, want 1 (new job must be dropped)", got)
	}
	if job := <-c.writeCh; job.entry != nil {
		t.Fatal("sentinel job was replaced; Store must not enqueue when full")
	}
}

// TestStore_RejectsToolTurn 验证工具轮不入库：工具结果依赖当次调用参数，
// 换个用户或换个时刻复用会给出自信但错误的答案。
func TestStore_RejectsToolTurn(t *testing.T) {
	cfg := testConfig()
	cfg.MinAnswerRunes = 10
	c := newTestCache(t, cfg)

	c.Store(context.Background(), StoreRequest{
		Scope:      testScope("0"),
		Query:      "帮我查一下这个订单的物流状态",
		Answer:     "这是工具返回的订单物流状态，不能跨请求复用。",
		Vector:     []float32{1, 0},
		Source:     "chat",
		ToolCalled: true,
	})

	// 准入在入队前同步完成，不需要等待 worker。
	if n, _ := c.Stats(); n != 0 {
		t.Fatalf("tool turn was stored: %d entries", n)
	}
}
