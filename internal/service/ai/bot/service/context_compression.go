package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"Logos/internal/bot/ctxmem"
	botmodel "Logos/internal/service/ai/bot/model"
	"Logos/pkg/logger"
	"Logos/pkg/obs"
	"Logos/pkg/singleflight"

	"github.com/cloudwego/eino/schema"
)

// ==================== 长对话上下文压缩 ====================
//
// 问题：固定截取最近 N 条消息，更早的对话直接丢弃，长对话会丢失关键背景。
//
// 策略（结构化滚动摘要）：
//  1. 按 token 预算从最新往回装 Working 层，装不下的早期消息进入摘要；
//  2. 摘要不再写散文，而是填 JSON 槽位：background/facts/decisions/
//     open_items/rejected，其中 facts 的 value 必须能在原文中找到；
//  3. 摘要状态放 SummaryStore（Redis 或进程内兜底），用
//     created_at 毫秒 + ID 做单调 CAS，避免并发写互相覆盖；
//  4. 正常路径走 stale-while-revalidate：旧摘要小于 10 分钟直接使用，
//     后台刷新；超过 10 分钟才同步摘要，且最多阻塞 3 秒；
//  5. 连续 3 次失败熔断 5 分钟，期间复用旧摘要，请求不受影响。

const (
	// historyPageSize 分页回溯时单次拉取的条数。
	historyPageSize = 100
	// historyFetchMax 单次请求最多回溯的条数，避免超长会话把 DB 拉爆。
	historyFetchMax = 500
	// minOverflowToSummarize 溢出消息少于此数不值得摘要。
	minOverflowToSummarize = 4
	// summaryRenderRuneLimit 单条消息进入摘要 prompt 的最大字符数。
	// 用 rune 而不是 byte：中文一字三字节，按字节切会把多字节序列拦腰截断。
	summaryRenderRuneLimit = 500
	// summaryMaxRunes 是 summarizeOverflow 兼容入口的正文上限。
	// 结构化摘要本身按 token 预算裁剪，这里保留旧入口的长度约束。
	summaryMaxRunes = 300
	// summarySyncTimeout 摘要不在请求关键路径上长时间阻塞。
	// 超过 3 秒就用旧摘要，本轮先保证回复。
	summarySyncTimeout = 3 * time.Second
	// summaryFillTimeout 后台回源的独立预算。singleflight 用
	// context.WithoutCancel，客户端断开不会取消其他等待者。
	summaryFillTimeout = 30 * time.Second
	// summaryStaleAfter 旧摘要小于这个年龄时直接使用并后台刷新。
	summaryStaleAfter = 10 * time.Minute
	// summaryBreakerThreshold 连续失败多少次后熔断。
	summaryBreakerThreshold = 3
	// summaryBreakerCooldown 熔断持续时间；期间只走 L2（复用旧摘要）。
	summaryBreakerCooldown = 5 * time.Minute
	// summaryStateTTL Redis 摘要状态的保留时间。
	summaryStateTTL = 30 * 24 * time.Hour
)

// contextSummaryPrompt 让模型把上一版结构化摘要与新增对话合并成新摘要。
//
// 关键差异：facts 的 value 要求原样抄写并带 evidence，供 §10.3 校验。
// 散文体摘要做不到这一点，因为它没有「字段」这个概念。
const contextSummaryPrompt = `你是一个对话压缩器。把「已有摘要」与「新增对话」合并成新摘要，只输出 JSON。

规则：
1. background：不超过 200 字，只写叙事脉络，不写具体编号。
2. facts：把订单号、金额、日期、单号、地址等原样抄进 value，禁止改写、禁止四舍五入。
3. evidence：填写该事实出现的消息序号（对话中 [N] 标注的数字）。
4. decisions / open_items / rejected：各不超过 5 条，每条不超过 30 字。
5. 没有内容的字段输出空数组，不要省略字段。
6. 只输出 JSON，不要 markdown 代码块。

JSON 结构：
{"background":"","facts":[{"key":"order_id","value":"","evidence":[1]}],"decisions":[],"open_items":[],"rejected":[]}

已有摘要：
%s

新增对话：
%s`

// ConversationCompressor 按会话维护结构化滚动摘要。
type ConversationCompressor struct {
	mu sync.RWMutex
	// store 是摘要状态的事实来源。默认进程内实现；
	// 生产环境由 NewBotService 替换为 Redis + 内存兜底。
	store ctxmem.SummaryStore
	// 同一会话的并发请求只回源一次；等待者各有自己的 ctx。
	summaryGroup *singleflight.Group[string]
	// failures 记录连续失败次数与熔断截止时间。只影响摘要，
	// 不影响主请求：熔断期间继续用旧摘要。
	failures map[string]summaryFailure
	// refreshing 防止每个请求都 spawn 一个后台刷新 goroutine。
	// singleflight 能合并回源，但不能阻止等待者堆积。
	refreshing sync.Map
	clock      func() time.Time
}

type summaryFailure struct {
	count     int
	openUntil time.Time
}

// ContextParts 一次装配的分层产物。
//
// 拆开返回是为了让调用方把 Retrieval 原文插在「摘要」与「Working 原文」
// 之间，同时拿到 Working 的消息 ID 做检索去重。
type ContextParts struct {
	Summary []*schema.Message
	Working []*schema.Message
	KeptIDs map[string]struct{}
}

func NewConversationCompressor() *ConversationCompressor {
	return NewConversationCompressorWithStore(ctxmem.NewMemorySummaryStore())
}

func NewConversationCompressorWithStore(store ctxmem.SummaryStore) *ConversationCompressor {
	if store == nil {
		store = ctxmem.NewMemorySummaryStore()
	}
	return &ConversationCompressor{
		store:        store,
		summaryGroup: singleflight.NewGroup[string](summaryFillTimeout),
		failures:     make(map[string]summaryFailure),
		clock:        time.Now,
	}
}

// SetStore 替换摘要状态存储。传 nil 时保持现有实现。
func (cc *ConversationCompressor) SetStore(store ctxmem.SummaryStore) {
	if cc == nil || store == nil {
		return
	}
	cc.mu.Lock()
	cc.store = store
	cc.mu.Unlock()
}

// chatFunc 摘要用的 LLM 调用（与主对话模型解耦，便于测试注入）。
type chatFunc func(ctx context.Context, prompt string) (string, error)

// BuildContext 构建分层上下文：摘要前缀 + 预算内 Working 原文。
func (cc *ConversationCompressor) BuildContext(
	ctx context.Context,
	conversationID string,
	msgs []*botmodel.Message,
	workingBudget, episodicBudget int,
	chat chatFunc,
) ContextParts {
	if len(msgs) == 0 {
		return ContextParts{}
	}

	kept, overflow := packMessagesByBudget(msgs, workingBudget)
	parts := ContextParts{
		Working: toSchemaMessages(kept),
		KeptIDs: make(map[string]struct{}, len(kept)),
	}
	for _, msg := range kept {
		if msg.ID != "" {
			parts.KeptIDs[msg.ID] = struct{}{}
		}
	}

	if len(overflow) >= minOverflowToSummarize && episodicBudget > 0 {
		background := cc.summaryFor(ctx, conversationID, overflow, episodicBudget, chat)
		if background != "" {
			// 以一轮对话注入背景，保持 user/assistant 交替。
			parts.Summary = []*schema.Message{
				schema.UserMessage("以下是本次对话此前的内容摘要，作为背景参考，无需回应：\n" + background),
				schema.AssistantMessage("好的，我已了解之前对话的背景。", nil),
			}
		}
	}
	return parts
}

// BuildContextMessages 保留原有调用方式：摘要前缀 + Working 原文。
func (cc *ConversationCompressor) BuildContextMessages(
	ctx context.Context,
	conversationID string,
	msgs []*botmodel.Message,
	workingBudget, episodicBudget int,
	chat chatFunc,
) []*schema.Message {
	parts := cc.BuildContext(ctx, conversationID, msgs, workingBudget, episodicBudget, chat)
	if len(parts.Summary) == 0 && len(parts.Working) == 0 {
		return nil
	}
	out := make([]*schema.Message, 0, len(parts.Summary)+len(parts.Working))
	out = append(out, parts.Summary...)
	out = append(out, parts.Working...)
	return out
}

// SummaryTokens 返回当前摘要注入后的估算 token（含 user/assistant 两条消息开销）。
// 调用方用它作为 Episodic 层的 Demand。
func (cc *ConversationCompressor) SummaryTokens(ctx context.Context, conversationID string) int {
	state, ok, err := cc.loadState(ctx, conversationID)
	if err != nil || !ok {
		return 0
	}
	text, _ := ctxmem.RenderStructured(state.DecodeSummary(), ctxmem.SummaryBackgroundTokenLimit*4, contextTokenEstimator)
	if text == "" {
		return 0
	}
	return ctxmem.EstimateText(text) + 100
}

// summaryFor 读取/刷新摘要并渲染为注入文本。
//
// 返回空字符串表示没有可用摘要；任何失败都只能降级，不能向上抛错。
func (cc *ConversationCompressor) summaryFor(
	ctx context.Context,
	conversationID string,
	overflow []*botmodel.Message,
	budget int,
	chat chatFunc,
) string {
	if chat == nil || len(overflow) == 0 {
		return ""
	}
	state, hasState, err := cc.loadState(ctx, conversationID)
	if err != nil {
		obs.RecordContextDegraded(ctx, ctxmem.LayerEpisodic.String(), "store_error")
	}
	rendered := cc.renderState(ctx, state, budget)

	newestID := overflow[0].ID
	if hasState && state.UpToMsgID == newestID {
		obs.RecordContextSummary(ctx, "cached")
		return rendered
	}
	if cc.breakerOpen(conversationID) {
		obs.RecordContextSummary(ctx, "breaker")
		return rendered
	}

	// 旧摘要还新鲜：先返回旧版本，后台刷新。正常路径下用户不等摘要。
	if hasState && cc.clock().Sub(state.UpdatedAt) < summaryStaleAfter {
		cc.refreshAsync(conversationID, overflow, chat)
		obs.RecordContextSummary(ctx, "cached")
		return rendered
	}

	syncCtx, cancel := context.WithTimeout(ctx, summarySyncTimeout)
	defer cancel()
	text, _, err := cc.summaryGroup.Do(syncCtx, conversationID, func(fillCtx context.Context) (string, error) {
		return cc.refreshSummary(fillCtx, conversationID, overflow, budget, chat)
	})
	if err != nil {
		obs.RecordContextDegraded(ctx, ctxmem.LayerEpisodic.String(), summaryReason(err))
		// L2：失败时复用上一版摘要，溢出部分下一轮再合并。
		return rendered
	}
	if text == "" {
		return rendered
	}
	return text
}

// refreshAsync 后台刷新摘要。每个会话同一时刻最多一个刷新 goroutine。
func (cc *ConversationCompressor) refreshAsync(conversationID string, overflow []*botmodel.Message, chat chatFunc) {
	if _, loaded := cc.refreshing.LoadOrStore(conversationID, struct{}{}); loaded {
		return
	}
	go func() {
		defer cc.refreshing.Delete(conversationID)
		ctx, cancel := context.WithTimeout(context.Background(), summaryFillTimeout)
		defer cancel()
		_, _, _ = cc.summaryGroup.Do(ctx, conversationID, func(fillCtx context.Context) (string, error) {
			return cc.refreshSummary(fillCtx, conversationID, overflow, summaryBudgetForAsync(), chat)
		})
	}()
}

func summaryBudgetForAsync() int {
	return ctxmem.SummaryBackgroundTokenLimit * 4
}

// refreshSummary 在 singleflight 的回源函数里执行：读取最新摘要点、
// 生成合并摘要、校验事实并以 CAS 发布。模型调用全程不持锁。
func (cc *ConversationCompressor) refreshSummary(
	ctx context.Context,
	conversationID string,
	overflow []*botmodel.Message,
	budget int,
	chat chatFunc,
) (string, error) {
	if cc.breakerOpen(conversationID) {
		return "", errSummaryBreakerOpen
	}

	state, _, err := cc.loadState(ctx, conversationID)
	if err != nil {
		cc.recordFailure(conversationID)
		return "", err
	}
	fresh := freshMessages(overflow, state.UpToMsgID)
	if len(fresh) == 0 {
		return cc.renderState(ctx, state, budget), nil
	}

	previous := state.DecodeSummary()
	previousJSON, _ := json.Marshal(previous)
	prompt := fmt.Sprintf(contextSummaryPrompt, string(previousJSON), renderMessagesWithEvidence(fresh))

	raw, err := chat(ctx, prompt)
	if err != nil {
		cc.recordFailure(conversationID)
		return "", err
	}

	summary, parseErr := ctxmem.ParseStructuredSummary(raw)
	if parseErr != nil {
		// L1：解析失败但拿到了文本，用纯文本当 background，facts 为空。
		summary = ctxmem.StructuredSummary{Background: truncateRunes(strings.TrimSpace(raw), summaryMaxRunes)}
		obs.RecordContextSummary(ctx, "parse_error")
	} else {
		obs.RecordContextSummary(ctx, "ok")
	}

	corpus := factCorpus(previous, fresh)
	kept, dropped := ctxmem.ValidateFacts(summary.Facts, corpus)
	for _, key := range dropped {
		obs.RecordContextSummaryFactDrop(ctx, key, 1)
	}
	summary.Facts = ctxmem.TrimFacts(kept, ctxmem.SummaryMaxFacts)

	payload, err := json.Marshal(summary)
	if err != nil {
		cc.recordFailure(conversationID)
		return "", err
	}
	newest := overflow[0]
	next := ctxmem.SummaryState{
		ConversationID: conversationID,
		Version:        ctxmem.SummarySchemaVersion,
		UpToMsgID:      newest.ID,
		Order: ctxmem.SummaryOrder{
			CreatedMS: newest.CreatedAt.UnixMilli(),
			MsgID:     newest.ID,
		},
		Payload:   payload,
		UpdatedAt: cc.clock(),
	}
	saved, err := cc.saveState(ctx, next)
	if err != nil {
		cc.recordFailure(conversationID)
		return "", err
	}
	if !saved {
		// 另一个副本已经写入了更晚的状态：以存储里的版本为准。
		latest, ok, loadErr := cc.loadState(ctx, conversationID)
		if loadErr == nil && ok {
			return cc.renderState(ctx, latest, budget), nil
		}
	}
	cc.recordSuccess(conversationID)

	logger.Info("对话上下文已压缩",
		logger.StringField("conversation_id", conversationID),
		logger.IntField("overflow_count", len(overflow)),
		logger.IntField("fresh_count", len(fresh)),
		logger.IntField("facts", len(summary.Facts)))

	text, _ := ctxmem.RenderStructured(summary, budget, contextTokenEstimator)
	return text, nil
}

// summaryChat 摘要用的 LLM 调用：使用全局 EinoManager
// （eino.Chat 约定首条为 system、其余 user/assistant 交替）。
//
// 摘要模型和主对话模型共用同一个实例，但 prompt 完全不同：
// 这里只要求「按 JSON 契约输出」，不参与用户的对话人格。
func (s *botServiceImpl) summaryChat(ctx context.Context, prompt string) (string, error) {
	if s.einoManager == nil || !s.einoManager.HasChatModel() {
		return "", errors.New("无可用的摘要模型")
	}
	return s.einoManager.Chat(ctx, []string{
		"你是尽职的对话压缩器，只输出 JSON 对象，不要任何前缀或格式标记。",
		prompt,
	})
}

// summarizeOverflow 是兼容旧调用/测试的入口，返回纯文本摘要。
//
// 新代码请使用 BuildContext + ContextParts，它能把 Retrieval 原文
// 插到摘要与 Working 之间。
func (cc *ConversationCompressor) summarizeOverflow(
	ctx context.Context, conversationID string, overflow []*botmodel.Message, chat chatFunc,
) string {
	text := cc.summaryFor(ctx, conversationID, overflow, summaryBudgetForAsync(), chat)
	return truncateRunes(text, summaryMaxRunes-3)
}

func (cc *ConversationCompressor) renderState(ctx context.Context, state ctxmem.SummaryState, budget int) string {
	if state.Version == 0 && len(state.Payload) == 0 {
		return ""
	}
	text, omissions := ctxmem.RenderStructured(state.DecodeSummary(), budget, contextTokenEstimator)
	for _, omission := range omissions {
		obs.RecordContextOmitted(ctx, omission.Layer.String(), omission.Reason, omission.Count)
	}
	return text
}

func (cc *ConversationCompressor) loadState(ctx context.Context, conversationID string) (ctxmem.SummaryState, bool, error) {
	cc.mu.RLock()
	store := cc.store
	cc.mu.RUnlock()
	if store == nil {
		return ctxmem.SummaryState{}, false, nil
	}
	return store.Load(ctx, conversationID)
}

func (cc *ConversationCompressor) saveState(ctx context.Context, state ctxmem.SummaryState) (bool, error) {
	cc.mu.RLock()
	store := cc.store
	cc.mu.RUnlock()
	if store == nil {
		return false, nil
	}
	return store.Save(ctx, state)
}

func (cc *ConversationCompressor) breakerOpen(conversationID string) bool {
	cc.mu.RLock()
	defer cc.mu.RUnlock()
	failure, ok := cc.failures[conversationID]
	return ok && cc.clock().Before(failure.openUntil)
}

func (cc *ConversationCompressor) recordFailure(conversationID string) {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	failure := cc.failures[conversationID]
	failure.count++
	if failure.count >= summaryBreakerThreshold {
		failure.count = 0
		failure.openUntil = cc.clock().Add(summaryBreakerCooldown)
	}
	cc.failures[conversationID] = failure
}

func (cc *ConversationCompressor) recordSuccess(conversationID string) {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	delete(cc.failures, conversationID)
}

var errSummaryBreakerOpen = errors.New("ctxmem: 摘要熔断中")

func summaryReason(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, errSummaryBreakerOpen):
		return "breaker"
	default:
		return "error"
	}
}

// freshMessages 取上次摘要点之后的新消息（overflow 为时间倒序）。
// 找不到旧摘要点时视为首次摘要，全量返回。
func freshMessages(overflow []*botmodel.Message, upToMsgID string) []*botmodel.Message {
	if upToMsgID == "" {
		return overflow
	}
	fresh := make([]*botmodel.Message, 0, len(overflow))
	for _, message := range overflow {
		if message.ID == upToMsgID {
			break
		}
		fresh = append(fresh, message)
	}
	return fresh
}

// factCorpus 是事实校验的语料：上一版已校验过的事实 + 本轮新增原文。
//
// 只拼本轮原文会把上一版保留下来的事实全部误杀——它们本来就在
// 更早的消息里，不在 fresh 中。
func factCorpus(previous ctxmem.StructuredSummary, fresh []*botmodel.Message) string {
	var builder strings.Builder
	for _, fact := range previous.Facts {
		builder.WriteString(fact.Value)
		builder.WriteString("\n")
	}
	for _, message := range fresh {
		builder.WriteString(message.Content)
		builder.WriteString("\n")
	}
	return builder.String()
}

// messagePageFetcher 按游标拉取一页消息，返回结果必须是 created_at DESC。
// before 为 nil 表示从最新一页开始。
type messagePageFetcher func(before *time.Time, limit int) ([]*botmodel.Message, error)

// collectHistoryPages 从最新消息开始分页回溯，直到装满 token 目标或触达条数上限。
//
// tokenTarget<=0 表示只受 maxMessages 限制。检索层需要完整窗口建索引，
// 不能在第一页满足 Working 预算时就停。
func collectHistoryPages(
	fetch messagePageFetcher,
	pageSize, maxMessages, tokenTarget int,
) ([]*botmodel.Message, error) {
	if pageSize <= 0 || maxMessages <= 0 {
		return nil, nil
	}

	all := make([]*botmodel.Message, 0, minInt(maxMessages, pageSize))
	seen := make(map[string]struct{}, pageSize)
	var before *time.Time
	tokens := 0

	for len(all) < maxMessages {
		page, err := fetch(before, pageSize)
		if err != nil {
			return nil, err
		}
		if len(page) == 0 {
			break
		}

		added := 0
		for _, msg := range page {
			if msg == nil {
				continue
			}
			if msg.ID != "" {
				if _, ok := seen[msg.ID]; ok {
					continue
				}
				seen[msg.ID] = struct{}{}
			}
			all = append(all, msg)
			tokens += estimateTokenCount(msg.Content)
			added++
			if len(all) >= maxMessages {
				break
			}
		}

		// 整页都是已读消息：游标无法继续前进，再查只会得到同一批数据。
		if added == 0 {
			break
		}
		if len(page) < pageSize {
			break
		}
		if len(all) >= maxMessages || (tokenTarget > 0 && tokens >= tokenTarget) {
			break
		}

		cursor := page[len(page)-1].CreatedAt.Add(time.Nanosecond)
		before = &cursor
	}

	return all, nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// packMessagesByBudget 按 token 预算从最新往回装载消息（纯函数，可单测）。
// msgs 为按时间倒序（最新在前）；返回 kept（预算内，按时间正序）、
// overflow（预算外更早的消息，按时间倒序）。
func packMessagesByBudget(msgs []*botmodel.Message, budgetTokens int) (kept, overflow []*botmodel.Message) {
	if len(msgs) == 0 {
		return nil, nil
	}
	chronological := toCtxmemMessages(msgs)
	keptMessages, overflowMessages, _ := ctxmem.PackWorking(chronological, budgetTokens, contextTokenEstimator)
	return fromCtxmemMessages(keptMessages), reverseChronological(fromCtxmemMessages(overflowMessages))
}

// reverseChronological 倒序（最新在前）转为时间正序（最早在前）
func reverseChronological(msgs []*botmodel.Message) []*botmodel.Message {
	out := make([]*botmodel.Message, len(msgs))
	for i, m := range msgs {
		out[len(msgs)-1-i] = m
	}
	return out
}

// toSchemaMessages 转 eino 消息（跳过空 assistant）。
//
// 连续同角色消息必须合并而不是丢弃：IM 里用户连发「订单号 20261106001」
// 和「帮我查下」是常态，丢掉第一条会让模型在缺少关键信息的情况下作答，
// 而且这种丢失不报错、不进摘要，线上很难定位。
func toSchemaMessages(msgs []*botmodel.Message) []*schema.Message {
	var out []*schema.Message
	var lastRole string
	for _, msg := range msgs {
		switch msg.Role {
		case "user":
			if msg.Content == "" {
				continue
			}
			if lastRole == "user" && len(out) > 0 {
				out[len(out)-1].Content = mergeContent(out[len(out)-1].Content, msg.Content)
				continue
			}
			out = append(out, schema.UserMessage(msg.Content))
			lastRole = "user"
		case "assistant":
			if msg.Content == "" {
				continue
			}
			if lastRole == "assistant" && len(out) > 0 {
				out[len(out)-1].Content = mergeContent(out[len(out)-1].Content, msg.Content)
				continue
			}
			out = append(out, schema.AssistantMessage(msg.Content, nil))
			lastRole = "assistant"
		}
	}
	return out
}

// mergeContent 用换行拼接同角色消息，保留两条消息原来的边界。
func mergeContent(prev, next string) string {
	if prev == "" {
		return next
	}
	if next == "" {
		return prev
	}
	return prev + "\n" + next
}

// renderMessages 把消息渲染为摘要 prompt 中的文本（旧测试兼容入口）。
func renderMessages(msgs []*botmodel.Message) string {
	var out strings.Builder
	for _, m := range msgs {
		role := "助手"
		if m.Role == "user" {
			role = "用户"
		}
		out.WriteString(role)
		out.WriteString(": ")
		out.WriteString(truncateRunes(m.Content, summaryRenderRuneLimit))
		out.WriteString("\n")
	}
	return out.String()
}

// renderMessagesWithEvidence 按时间正序渲染，并给每条消息编号。
//
// 编号是 facts.evidence 的锚点：模型必须能说清「这条事实来自第几条消息」，
// 否则事实校验只能校验值，无法定位来源。
func renderMessagesWithEvidence(msgs []*botmodel.Message) string {
	chronological := make([]*botmodel.Message, 0, len(msgs))
	for i := len(msgs) - 1; i >= 0; i-- {
		chronological = append(chronological, msgs[i])
	}
	var out strings.Builder
	for i, m := range chronological {
		role := "助手"
		if m.Role == "user" {
			role = "用户"
		}
		fmt.Fprintf(&out, "[%d] %s: %s\n", i+1, role, truncateRunes(m.Content, summaryRenderRuneLimit))
	}
	return out.String()
}

// truncateRunes 按字符（rune）截断，保证输出始终是合法 UTF-8。
func truncateRunes(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	runes := []rune(s)
	return string(runes[:limit]) + "..."
}

// contextTokenEstimator 统一估算口径；测试可替换为固定值实现。
var contextTokenEstimator ctxmem.TokenEstimator = ctxmem.DefaultEstimator{}

// toCtxmemMessages 把存储模型转成装配层的消息视图（DESC → 时间正序）。
//
// TurnIndex 在转换时按 user 消息累计，供检索出处与事实槽对齐。
// 没有它，召回只能标「消息 ID」，用户看不懂「第几条」。
func toCtxmemMessages(msgs []*botmodel.Message) []ctxmem.Message {
	out := make([]ctxmem.Message, 0, len(msgs))
	turn := 0
	for i := len(msgs) - 1; i >= 0; i-- {
		msg := msgs[i]
		if msg == nil {
			continue
		}
		if msg.Role == "user" {
			turn++
		}
		out = append(out, ctxmem.Message{
			ID:        msg.ID,
			Role:      msg.Role,
			Content:   msg.Content,
			CreatedAt: msg.CreatedAt,
			TurnIndex: turn,
		})
	}
	return out
}

func fromCtxmemMessages(messages []ctxmem.Message) []*botmodel.Message {
	out := make([]*botmodel.Message, 0, len(messages))
	for _, message := range messages {
		out = append(out, &botmodel.Message{
			ID:        message.ID,
			Role:      message.Role,
			Content:   message.Content,
			CreatedAt: message.CreatedAt,
		})
	}
	return out
}
