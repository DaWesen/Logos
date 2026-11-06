package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"Logos/internal/bot/ctxmem"
	botmodel "Logos/internal/service/ai/bot/model"
	"Logos/pkg/logger"
	"Logos/pkg/singleflight"

	"github.com/cloudwego/eino/schema"
)

// ==================== 长对话上下文压缩 ====================
//
// 问题：buildHistoryMessages 固定截取最近 N 条消息，更早的对话直接丢弃，
// 长对话（客服、长期顾问场景）会丢失关键背景。
//
// 策略（rolling summary）：
//  1. 取比窗口更多的历史消息，按 token 预算从最新往回装载；
//  2. 装不下的早期消息不丢弃，而是增量摘要为「对话背景」——
//     摘要按会话缓存，只对"上次摘要点之后新溢出"的消息做一次
//     合并摘要，均摊成本为 O(溢出消息数)；
//  3. 摘要以一轮 user/assistant 消息的形式注入对话开头，
//     不破坏后续 user/assistant 交替结构；
//  4. 摘要 LLM 调用失败时降级为直接截断（即原有行为），主流程不受影响。

const (
	// historyPageSize 分页回溯时单次拉取的条数。
	historyPageSize = 100
	// historyFetchMax 单次请求最多回溯的条数，避免超长会话把 DB 拉爆。
	historyFetchMax = 500
	// minOverflowToSummarize 溢出消息少于此数不值得摘要
	minOverflowToSummarize = 4
	// summaryRenderRuneLimit 单条消息进入摘要 prompt 的最大字符数。
	// 用 rune 而不是 byte：中文一字三字节，按字节切会把多字节序列拦腰截断。
	summaryRenderRuneLimit = 500
	// summaryMaxRunes 摘要正文的硬上限。prompt 里的「300 字以内」只是请求，
	// 模型并不保证遵守；不在这里截断，摘要会随轮次膨胀并反过来挤占历史预算。
	summaryMaxRunes = 300
	// summaryTimeout 单次摘要 LLM 调用的超时；singleflight 的回源也必须受同一上限约束。
	summaryTimeout = 30 * time.Second
)

// contextSummaryPrompt 让 LLM 把早期对话合并为简洁背景
const contextSummaryPrompt = `请把以下「已有对话摘要」与「新增对话片段」合并为一份新的对话摘要。
要求：
1. 保留关键事实、用户偏好、已做出的结论与未完成事项
2. 丢弃寒暄与冗余细节
3. 用第三人称简洁叙述，控制在 300 字以内
4. 直接输出摘要正文，不要任何前言或格式标记

已有对话摘要：
%s

新增对话片段：
%s`

// ConversationCompressor 按会话维护滚动摘要
type ConversationCompressor struct {
	mu sync.RWMutex
	// conversationID -> 摘要状态（进程内缓存，重启后降级为直接截断）。
	// 存值而不是指针：摘要完成后整体替换，读者不会看到被并发改写的半成品。
	states map[string]summaryState
	// 同一会话的并发请求只回源一次；等待者各有自己的 ctx，客户端断开不会
	// 把其他请求一起取消。
	summaryGroup *singleflight.Group[string]
}

type summaryState struct {
	summary   string // 早期对话的滚动摘要
	upToMsgID string // 已摘要到的最后一条消息 ID
	createdAt time.Time
}

func NewConversationCompressor() *ConversationCompressor {
	return &ConversationCompressor{
		states:       make(map[string]summaryState),
		summaryGroup: singleflight.NewGroup[string](summaryTimeout),
	}
}

// chatFunc 摘要用的 LLM 调用（与主对话模型解耦，便于测试注入）
type chatFunc func(ctx context.Context, prompt string) (string, error)

// BuildContextMessages 构建带上下文压缩的对话消息：
// 预算内按轮装最近消息，溢出部分滚动摘要为对话背景。
//
// msgs 必须是 created_at DESC（与 repo.GetMessages 一致）。预算由调用方
// 从模型窗口推导后传入，压缩器不再持有任何写死的 6000/12000 常量。
// workingBudget 与 episodicBudget 的单位都是 token，且已扣除消息固定开销。
func (cc *ConversationCompressor) BuildContextMessages(
	ctx context.Context,
	conversationID string,
	msgs []*botmodel.Message,
	workingBudget, episodicBudget int,
	chat chatFunc,
) []*schema.Message {
	if len(msgs) == 0 {
		return nil
	}

	kept, overflow := packMessagesByBudget(msgs, workingBudget)

	var background string
	if len(overflow) >= minOverflowToSummarize && episodicBudget > 0 {
		background = cc.summarizeOverflow(ctx, conversationID, overflow, chat)
		background = ctxmem.TruncateToTokens(background, episodicBudget, contextTokenEstimator)
	}

	out := toSchemaMessages(kept)

	if background != "" {
		// 以一轮对话注入背景，保持 user/assistant 交替
		out = append([]*schema.Message{
			schema.UserMessage("以下是本次对话此前的内容摘要，作为背景参考，无需回应：\n" + background),
			schema.AssistantMessage("好的，我已了解之前对话的背景。", nil),
		}, out...)
	}
	return out
}

// SummaryTokens 返回当前摘要注入后的估算 token（含 user/assistant 两条消息开销）。
// 调用方用它作为 Episodic 层的 Demand。
func (cc *ConversationCompressor) SummaryTokens(conversationID string) int {
	cc.mu.RLock()
	defer cc.mu.RUnlock()
	state, ok := cc.states[conversationID]
	if !ok || state.summary == "" {
		return 0
	}
	return ctxmem.EstimateText(state.summary) + 100
}

// messagePageFetcher 按游标拉取一页消息，返回结果必须是 created_at DESC。
// before 为 nil 表示从最新一页开始。
type messagePageFetcher func(before *time.Time, limit int) ([]*botmodel.Message, error)

// collectHistoryPages 从最新消息开始分页回溯，直到装满 token 目标或触达条数上限。
//
// 旧实现只取最近 60 条：这 60 条通常装不满 6000 token 预算，overflow 恒为空，
// summarizeOverflow 永远不会执行，第 61 条以前的消息既没有原文也没有摘要。
// 分页后每次多取一段，压缩层才真正有「溢出内容」可摘要。
//
// 游标用 oldest.CreatedAt + 1ns 而不是直接用它本身：created_at 精度可能低于
// 单条消息的写入间隔，同一毫秒内的多条消息会被 `<` 条件整批跳过。代价是下一页
// 可能重复返回同时间戳的消息，因此按 ID 去重；若整页都是重复（极端情况下同一
// 时间戳超过一页），主动停止，避免游标不前进造成死循环。
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
		if len(all) >= maxMessages || tokens >= tokenTarget {
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

// summarizeOverflow 把新溢出的消息与旧摘要合并，生成滚动摘要
func (cc *ConversationCompressor) summarizeOverflow(
	ctx context.Context, conversationID string, overflow []*botmodel.Message, chat chatFunc,
) string {
	if chat == nil {
		return ""
	}

	summary, _, err := cc.summaryGroup.Do(ctx, conversationID, func(fillCtx context.Context) (string, error) {
		return cc.refreshSummary(fillCtx, conversationID, overflow, chat)
	})
	if err != nil {
		// 摘要失败降级：丢弃溢出（等同直接截断）。等待者拿到同一个错误，
		// 不会各自再打一次已经失败的模型。
		logger.Warn("对话上下文摘要失败，降级为截断",
			logger.StringField("conversation_id", conversationID),
			logger.ErrorField(err))
		return ""
	}
	return summary
}

// refreshSummary 在 singleflight 的回源函数里执行，负责读取最新摘要点、
// 生成合并摘要并以整体替换的方式发布。状态只在锁内读写，模型调用不持锁。
func (cc *ConversationCompressor) refreshSummary(
	ctx context.Context, conversationID string, overflow []*botmodel.Message, chat chatFunc,
) (string, error) {
	cc.mu.RLock()
	state, hasState := cc.states[conversationID]
	cc.mu.RUnlock()

	// 找出上次摘要点之后新溢出的消息
	var fresh []*botmodel.Message
	if hasState {
		for _, m := range overflow {
			if m.ID == state.upToMsgID {
				break
			}
			fresh = append(fresh, m)
		}
		// 无新溢出：直接复用已有摘要
		if len(fresh) == 0 {
			return state.summary, nil
		}
	} else {
		fresh = overflow
	}

	prevSummary := "（无，本次为首次摘要）"
	if hasState && state.summary != "" {
		prevSummary = state.summary
	}

	prompt := fmt.Sprintf(contextSummaryPrompt, prevSummary, renderMessages(fresh))

	summaryCtx, cancel := context.WithTimeout(ctx, summaryTimeout)
	defer cancel()
	summary, err := chat(summaryCtx, prompt)
	if err != nil {
		return "", err
	}

	summary = strings.TrimSpace(summary)
	if summary == "" {
		return "", errors.New("摘要模型返回空内容")
	}
	// 保留 3 个 rune 给省略号，确保最终正文不超过 300 字。
	summary = truncateRunes(summary, summaryMaxRunes-3)

	cc.mu.Lock()
	cc.states[conversationID] = summaryState{
		summary:   summary,
		upToMsgID: overflow[0].ID, // overflow[0] 是溢出区最新的一条
		createdAt: time.Now(),
	}
	cc.mu.Unlock()

	logger.Info("对话上下文已压缩",
		logger.StringField("conversation_id", conversationID),
		logger.IntField("overflow_count", len(overflow)),
		logger.IntField("fresh_count", len(fresh)))
	return summary, nil
}

// summaryChat 摘要用的 LLM 调用：使用全局 EinoManager
// （eino.Chat 约定首条为 system、其余 user/assistant 交替）
func (s *botServiceImpl) summaryChat(ctx context.Context, prompt string) (string, error) {
	if s.einoManager == nil || !s.einoManager.HasChatModel() {
		return "", errors.New("无可用的摘要模型")
	}
	return s.einoManager.Chat(ctx, []string{
		"你是尽职的对话摘要助手，只输出摘要正文，不要任何前缀或格式标记。",
		prompt,
	})
}

// packMessagesByBudget 按 token 预算从最新往回装载消息（纯函数，可单测）。
// msgs 为按时间倒序（最新在前）；返回 kept（预算内，按时间正序）、
// overflow（预算外更早的消息，按时间倒序）。
//
// 装载单位是「轮」：assistant(tool_calls) 与它的 tool 结果必须同进同出。
// 最新一轮单独超预算时，ctxmem 会保留最后一条并做 rune-safe 截断，
// 保证当前问题不丢，同时不突破 I1。
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

// renderMessages 把消息渲染为摘要 prompt 中的文本
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
func toCtxmemMessages(msgs []*botmodel.Message) []ctxmem.Message {
	out := make([]ctxmem.Message, 0, len(msgs))
	for i := len(msgs) - 1; i >= 0; i-- {
		msg := msgs[i]
		if msg == nil {
			continue
		}
		out = append(out, ctxmem.Message{
			ID:        msg.ID,
			Role:      msg.Role,
			Content:   msg.Content,
			CreatedAt: msg.CreatedAt,
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
