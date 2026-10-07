package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	botmodel "Logos/internal/service/ai/bot/model"
	"Logos/pkg/logger"

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
	// historyTokenBudget 历史消息的 token 预算（输入侧粗估，不含 system prompt）
	historyTokenBudget = 6000
	// historyFetchCount 预算内尽量多取历史用于装载与摘要判断
	historyFetchCount = 60
	// minOverflowToSummarize 溢出消息少于此数不值得摘要
	minOverflowToSummarize = 4
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
	// conversationID -> 摘要状态（进程内缓存，重启后降级为直接截断）
	states map[string]*summaryState
}

type summaryState struct {
	summary   string    // 早期对话的滚动摘要
	upToMsgID string    // 已摘要到的最后一条消息 ID
	createdAt time.Time
}

func NewConversationCompressor() *ConversationCompressor {
	return &ConversationCompressor{states: make(map[string]*summaryState)}
}

// chatFunc 摘要用的 LLM 调用（与主对话模型解耦，便于测试注入）
type chatFunc func(ctx context.Context, prompt string) (string, error)

// BuildContextMessages 构建带上下文压缩的对话消息：
// 预算内装最近消息，溢出部分滚动摘要为对话背景。
// fetchMessages 拉取该会话最近的消息（按时间倒序）。
func (cc *ConversationCompressor) BuildContextMessages(
	ctx context.Context,
	conversationID string,
	fetchMessages func() ([]*botmodel.Message, error),
	chat chatFunc,
) []*schema.Message {
	msgs, err := fetchMessages()
	if err != nil || len(msgs) == 0 {
		return nil
	}

	kept, overflow := packMessagesByBudget(msgs, historyTokenBudget)

	var background string
	if len(overflow) >= minOverflowToSummarize {
		background = cc.summarizeOverflow(ctx, conversationID, overflow, chat)
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

// summarizeOverflow 把新溢出的消息与旧摘要合并，生成滚动摘要
func (cc *ConversationCompressor) summarizeOverflow(
	ctx context.Context, conversationID string, overflow []*botmodel.Message, chat chatFunc,
) string {
	if chat == nil {
		return ""
	}

	cc.mu.RLock()
	state := cc.states[conversationID]
	cc.mu.RUnlock()

	// 找出上次摘要点之后新溢出的消息
	var fresh []*botmodel.Message
	if state != nil {
		for _, m := range overflow {
			if m.ID == state.upToMsgID {
				break
			}
			fresh = append(fresh, m)
		}
		// 无新溢出：直接复用已有摘要
		if len(fresh) == 0 {
			return state.summary
		}
	} else {
		fresh = overflow
	}

	prevSummary := "（无，本次为首次摘要）"
	if state != nil && state.summary != "" {
		prevSummary = state.summary
	}

	prompt := fmt.Sprintf(contextSummaryPrompt, prevSummary, renderMessages(fresh))

	summaryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	summary, err := chat(summaryCtx, prompt)
	if err != nil {
		// 摘要失败降级：丢弃溢出（等同直接截断）
		logger.Warn("对话上下文摘要失败，降级为截断",
			logger.StringField("conversation_id", conversationID),
			logger.ErrorField(err))
		return ""
	}

	if state == nil {
		state = &summaryState{}
	}
	state.summary = summary
	state.upToMsgID = overflow[0].ID // overflow[0] 是溢出区最新的一条
	state.createdAt = time.Now()

	cc.mu.Lock()
	cc.states[conversationID] = state
	cc.mu.Unlock()

	logger.Info("对话上下文已压缩",
		logger.StringField("conversation_id", conversationID),
		logger.IntField("overflow_count", len(overflow)),
		logger.IntField("fresh_count", len(fresh)))
	return summary
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
// 最新的第一条消息无条件保留（即使单条超预算），保证当前对话不丢。
func packMessagesByBudget(msgs []*botmodel.Message, budgetTokens int) (kept, overflow []*botmodel.Message) {
	used := 0
	for i, m := range msgs {
		cost := estimateTokenCount(m.Content)
		if i > 0 && used+cost > budgetTokens {
			// 从这条开始全部溢出
			return reverseChronological(msgs[:i]), msgs[i:]
		}
		used += cost
	}
	return reverseChronological(msgs), nil
}

// reverseChronological 倒序（最新在前）转为时间正序（最早在前）
func reverseChronological(msgs []*botmodel.Message) []*botmodel.Message {
	out := make([]*botmodel.Message, len(msgs))
	for i, m := range msgs {
		out[len(msgs)-1-i] = m
	}
	return out
}

// toSchemaMessages 转 eino 消息（跳过连续同角色与空 assistant）
func toSchemaMessages(msgs []*botmodel.Message) []*schema.Message {
	var out []*schema.Message
	var lastRole string
	for _, msg := range msgs {
		switch msg.Role {
		case "user":
			if lastRole == "user" {
				continue
			}
			out = append(out, schema.UserMessage(msg.Content))
			lastRole = "user"
		case "assistant":
			if msg.Content == "" {
				continue
			}
			out = append(out, schema.AssistantMessage(msg.Content, nil))
			lastRole = "assistant"
		}
	}
	return out
}

// renderMessages 把消息渲染为摘要 prompt 中的文本
func renderMessages(msgs []*botmodel.Message) string {
	out := ""
	for _, m := range msgs {
		role := "助手"
		if m.Role == "user" {
			role = "用户"
		}
		content := m.Content
		if len(content) > 500 {
			content = content[:500] + "..."
		}
		out += role + ": " + content + "\n"
	}
	return out
}
