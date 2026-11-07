package service

import (
	"context"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"

	"Logos/internal/bot/ctxmem"
	"Logos/internal/service/ai/bot/dao"
	botmodel "Logos/internal/service/ai/bot/model"
)

// ctxTestRepo 是上下文装配所需的最小仓储实现。
//
// 嵌入 nil 接口是为了让任何未预期的方法调用立刻 panic：装配测试一旦
// 悄悄走到别的查询路径，就不再能证明被测代码只依赖这几条契约。
type ctxTestRepo struct {
	dao.BotRepository

	messages []*botmodel.Message
	slots    map[string]*botmodel.ConversationSlot
	slotSeq  int
}

func newCtxTestRepo() *ctxTestRepo {
	return &ctxTestRepo{slots: make(map[string]*botmodel.ConversationSlot)}
}

func (r *ctxTestRepo) GetMessages(
	_ context.Context, conversationID string, limit int, before *time.Time,
) ([]*botmodel.Message, error) {
	if limit <= 0 {
		limit = len(r.messages)
	}
	page := make([]*botmodel.Message, 0, limit)
	for i := len(r.messages) - 1; i >= 0 && len(page) < limit; i-- {
		message := r.messages[i]
		if message.ConversationID != conversationID {
			continue
		}
		if before != nil && !message.CreatedAt.Before(*before) {
			continue
		}
		page = append(page, message)
	}
	return page, nil
}

func (r *ctxTestRepo) CountUserTurns(_ context.Context, conversationID string) (int, error) {
	turns := 0
	for _, message := range r.messages {
		if message.ConversationID == conversationID && message.Role == "user" {
			turns++
		}
	}
	return turns, nil
}

// SaveConversationSlot 按 key 覆盖：同一句话重复装配不应让槽位数量增长，
// 否则 T9 的 100 次组装会因为仓储副作用而拿到不同输入。
func (r *ctxTestRepo) SaveConversationSlot(_ context.Context, slot *botmodel.ConversationSlot) error {
	key := slot.ConversationID + "\x00" + slot.Key
	if existing, ok := r.slots[key]; ok {
		existing.Value = slot.Value
		existing.Kind = slot.Kind
		existing.SourceMsgID = slot.SourceMsgID
		existing.SourceRole = slot.SourceRole
		existing.TurnIndex = slot.TurnIndex
		existing.Confidence = slot.Confidence
		return nil
	}
	r.slotSeq++
	slot.ID = fmt.Sprintf("slot-%03d", r.slotSeq)
	if slot.CreatedAt.IsZero() {
		slot.CreatedAt = time.Unix(0, 0)
	}
	r.slots[key] = slot
	return nil
}

func (r *ctxTestRepo) ListConversationSlots(
	_ context.Context, conversationID string,
) ([]*botmodel.ConversationSlot, error) {
	rows := make([]*botmodel.ConversationSlot, 0, len(r.slots))
	for _, slot := range r.slots {
		if slot.ConversationID == conversationID {
			rows = append(rows, slot)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Key < rows[j].Key })
	return rows, nil
}

func newCtxTestService(repo *ctxTestRepo) *botServiceImpl {
	return &botServiceImpl{
		repo:       repo,
		compressor: NewConversationCompressor(),
		indexCache: ctxmem.NewIndexCache(time.Minute, 8),
	}
}

func ctxTestBot(window int) *botmodel.Bot {
	return &botmodel.Bot{
		ID:           "bot-ctx",
		Type:         "chat",
		SystemPrompt: "你是一个严谨的客服助手。",
		Config:       botmodel.JSONMap{"context_window": fmt.Sprintf("%d", window)},
	}
}

func ctxTestMessage(id, role, content string, at time.Time) *botmodel.Message {
	return &botmodel.Message{
		ID:             id,
		ConversationID: "conv-ctx",
		Role:           role,
		Content:        content,
		CreatedAt:      at,
	}
}

// ctxTestHistory 生成按时间正序的真实对话：每轮 user + assistant，
// 最后追加当前用户消息（buildConversationContext 的历史里包含它）。
func ctxTestHistory(turns, contentLen int, query string) []*botmodel.Message {
	base := time.Unix(1_700_000_000, 0)
	messages := make([]*botmodel.Message, 0, turns*2+1)
	for i := 0; i < turns; i++ {
		messages = append(messages,
			ctxTestMessage(fmt.Sprintf("u-%04d", i), "user", strings.Repeat("问", contentLen), base.Add(time.Duration(2*i)*time.Second)),
			ctxTestMessage(fmt.Sprintf("a-%04d", i), "assistant", strings.Repeat("答", contentLen), base.Add(time.Duration(2*i+1)*time.Second)),
		)
	}
	messages = append(messages,
		ctxTestMessage("u-current", "user", query, base.Add(time.Duration(2*turns)*time.Second)))
	return messages
}

func ctxTestBudget(bot *botmodel.Bot, query string) int {
	systemTokens := ctxmem.EstimateText(bot.SystemPrompt) +
		ctxmem.EstimateText(query) +
		toolSchemaTokenAllowance
	return ctxmem.DeriveHistoryBudget(resolveContextWindow(bot), systemTokens).Total
}

func serializeContext(messages []*schema.Message) string {
	var builder strings.Builder
	for _, message := range messages {
		builder.WriteString(string(message.Role))
		builder.WriteByte('\x00')
		builder.WriteString(message.Content)
		builder.WriteByte('\x00')
	}
	return builder.String()
}

// ctxTestWorkingSlice 从装配结果里剥掉前缀层，只留下 Working 原文。
//
// 层顺序固定，且每个前缀层都是 user/assistant 两条消息：
// 事实槽以「以上是背景事实」结尾，摘要以固定前缀开头。这样 T2 量到的
// 就是「窗口变大后能多装多少原文」，而不是被摘要/事实层撑大的总量。
func ctxTestWorkingSlice(messages []*schema.Message) []*schema.Message {
	start := 0
	for start < len(messages) {
		content := messages[start].Content
		isSemantic := strings.Contains(content, "以上是背景事实")
		isSummary := strings.HasPrefix(content, "以下是本次对话此前的内容摘要")
		if !isSemantic && !isSummary {
			break
		}
		start += 2
	}
	return messages[start:]
}

// TestBuildConversationContext_PropertyBudget 对应规格 §15.1 T1：
// 随机 1–200 轮、随机长度消息，1000 次组装都不得 panic，且注入总量
// 不得超过按模型窗口推导出的历史预算。
func TestBuildConversationContext_PropertyBudget(t *testing.T) {
	rng := rand.New(rand.NewSource(20261107))
	windows := []int{4096, 8000, 32000, 128000}

	for i := 0; i < 1000; i++ {
		turns := 1 + rng.Intn(200)
		repo := newCtxTestRepo()
		repo.messages = ctxTestHistory(turns, 0, "请继续介绍这个方案")
		for j := range repo.messages {
			if repo.messages[j].Role == "user" && repo.messages[j].ID == "u-current" {
				continue
			}
			length := rng.Intn(400)
			if rng.Intn(2) == 0 {
				repo.messages[j].Content = strings.Repeat("a", length)
			} else {
				repo.messages[j].Content = strings.Repeat("中", length)
			}
		}

		bot := ctxTestBot(windows[i%len(windows)])
		query := "请继续介绍这个方案"
		userMsg := repo.messages[len(repo.messages)-1]
		svc := newCtxTestService(repo)

		out := svc.buildConversationContext(t.Context(), bot, "user-1", userMsg)
		if tokens := ctxmem.EstimateMessages(out); tokens > ctxTestBudget(bot, query) {
			t.Fatalf("iteration %d (turns=%d, window=%d): tokens=%d exceed budget=%d",
				i, turns, resolveContextWindow(bot), tokens, ctxTestBudget(bot, query))
		}
	}
}

// TestBuildConversationContext_WorkingGrowsWithWindow 对应 T2：
// 同一份历史在 8k / 32k / 128k 窗口下，Working 层的 token 必须单调递增。
// 这里刻意构造 480 条长消息，保证三个窗口都处于「装不完」的状态。
func TestBuildConversationContext_WorkingGrowsWithWindow(t *testing.T) {
	query := "请继续介绍这个方案"
	previous := 0
	for _, window := range []int{8000, 32000, 128000} {
		repo := newCtxTestRepo()
		repo.messages = ctxTestHistory(240, 200, query)
		svc := newCtxTestService(repo)
		bot := ctxTestBot(window)

		out := svc.buildConversationContext(t.Context(), bot, "user-1", repo.messages[len(repo.messages)-1])
		if len(out) == 0 {
			t.Fatalf("window %d: empty assembly", window)
		}
		working := ctxTestWorkingSlice(out)
		if len(working) == 0 {
			t.Fatalf("window %d: working layer is empty", window)
		}
		tokens := ctxmem.EstimateMessages(working)
		if tokens <= previous {
			t.Fatalf("window %d: working tokens = %d, want > %d", window, tokens, previous)
		}
		t.Logf("window=%d working_messages=%d working_tokens=%d", window, len(working), tokens)
		previous = tokens
	}
}

// TestBuildConversationContext_Deterministic100Runs 对应 T9：
// 同一输入连续组装 100 次，四层（事实槽 / 摘要 / 检索 / 原文）的输出
// 必须逐字节一致。
func TestBuildConversationContext_Deterministic100Runs(t *testing.T) {
	query := "那个订单 20261106001 到哪了"
	repo := newCtxTestRepo()
	repo.messages = ctxTestHistory(12, 60, query)
	repo.messages[0].Content = "订单号 20261106001，麻烦帮我跟进一下物流"

	orderSlot := &botmodel.ConversationSlot{
		ConversationID: "conv-ctx",
		Key:            "order_id",
		Value:          "20261106001",
		Kind:           "id",
		SourceMsgID:    "u-0000",
		SourceRole:     "user",
		TurnIndex:      1,
		Confidence:     0.95,
	}
	if err := repo.SaveConversationSlot(t.Context(), orderSlot); err != nil {
		t.Fatalf("seed slot: %v", err)
	}

	svc := newCtxTestService(repo)
	bot := ctxTestBot(32768)
	userMsg := repo.messages[len(repo.messages)-1]

	first := serializeContext(svc.buildConversationContext(t.Context(), bot, "user-1", userMsg))
	if first == "" {
		t.Fatal("assembly must not be empty")
	}
	for i := 1; i < 100; i++ {
		got := serializeContext(svc.buildConversationContext(t.Context(), bot, "user-1", userMsg))
		if got != first {
			t.Fatalf("run %d differs from first assembly", i)
		}
	}
}

// BenchmarkBuildConversationContext_800Turns 为规格 T14 提供可复现的
// 测量入口：800 轮真实历史（1601 条消息），预算 32k，不含摘要 LLM 调用。
//
// 这里只报告单次组装的耗时分布均值/分配，不声称已得到 P99；P99 需要在
// 目标部署机上用固定环境多轮采样后写入压测报告。
func BenchmarkBuildConversationContext_800Turns(b *testing.B) {
	repo := newCtxTestRepo()
	repo.messages = ctxTestHistory(800, 60, "请继续介绍这个方案")
	svc := newCtxTestService(repo)
	bot := ctxTestBot(32768)
	userMsg := repo.messages[len(repo.messages)-1]

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out := svc.buildConversationContext(context.Background(), bot, "user-1", userMsg)
		if len(out) == 0 {
			b.Fatal("empty assembly")
		}
	}
}
