package service

import (
	"context"
	"strconv"
	"strings"

	"Logos/internal/bot/ctxmem"
	botmodel "Logos/internal/service/ai/bot/model"
	"Logos/pkg/logger"

	"github.com/cloudwego/eino/schema"
)

const (
	// defaultContextWindowTokens 大多数 OpenAI 兼容模型的最低共同窗口。
	// 没配 context_window 的 Bot 用这个值，宁可少装历史也不要请求 400。
	defaultContextWindowTokens = 32768
	// minContextWindowTokens 低于这个值一定是配置错误，按默认窗口处理。
	minContextWindowTokens = 2048
	// minContextHistoryTokens 低于这个预算装不下「一轮问答 + 摘要开销」，
	// 继续压缩只会把当前问题截成半句，不如走简单对话。
	minContextHistoryTokens = 256
	// toolSchemaTokenAllowance 工具 schema 在 Agent 构建期生成，组装层拿不到
	// 精确 token。固定扣一笔保守额度，避免「预算刚好装满 + 工具定义」顶爆窗口。
	toolSchemaTokenAllowance = 512
)

const semanticAckText = "好的，我已了解本次会话中已确认的事实。"

// buildConversationContext 组装本轮请求的全部上下文消息。
//
// 顺序按「注意力优先级」固定为：长期记忆 → 会话事实槽 → 摘要 → 原文。
// 长期记忆与事实槽都是 per-user / per-conversation 的运行时数据，
// 必须走消息而不是 Agent 的 system prompt：Agent 按 bot.ID 缓存，
// 写进 system prompt 会把第一个用户的数据带给之后所有用户（D1）。
//
// 返回 nil 表示没有可注入的历史，调用方应走简单对话分支，
// 由 Agent 直接携带当前问题发起请求。
func (s *botServiceImpl) buildConversationContext(
	ctx context.Context, bot *botmodel.Bot, userID string, userMsg *botmodel.Message,
) []*schema.Message {
	if bot == nil || userMsg == nil || s.compressor == nil {
		return nil
	}
	conversationID := userMsg.ConversationID

	memoryMessages := s.buildMemoryContextMessages(ctx, bot, userID, userMsg.Content)

	// S 用本轮真实注入量估算：system prompt + 长期记忆 + 当前问题。
	// 当前问题也算进去，是为了让 H 先替它留好位置——Working 层按轮装载，
	// 如果问题本身就超过 Working 配额，模型只能看到被截断的半句话。
	systemTokens := ctxmem.EstimateText(bot.SystemPrompt) +
		ctxmem.EstimateMessages(memoryMessages) +
		ctxmem.EstimateText(userMsg.Content) +
		toolSchemaTokenAllowance

	derived := ctxmem.DeriveHistoryBudget(resolveContextWindow(bot), systemTokens)
	if derived.Total < minContextHistoryTokens {
		logger.Warn("上下文预算不足，降级为简单对话",
			logger.StringField("conversation_id", conversationID),
			logger.IntField("history_budget", derived.Total),
			logger.IntField("system_tokens", systemTokens))
		return nil
	}

	// 规则抽取同步执行：用户下一轮立刻问「刚才那个订单号」时，
	// 异步写库还没完成就会查不到，这个窗口是用户可感知的。
	slots := s.syncConversationSlots(ctx, userMsg)
	semanticProbe, _ := ctxmem.RenderSlots(slots, derived.Total, contextTokenEstimator)

	history, err := s.fetchHistoryMessages(ctx, conversationID, derived.Total)
	if err != nil {
		logger.Warn("拉取会话历史失败，降级为简单对话",
			logger.StringField("conversation_id", conversationID),
			logger.ErrorField(err))
		return nil
	}
	if len(history) == 0 {
		return nil
	}

	var demand [4]int
	demand[ctxmem.LayerWorking] = ctxmem.MessagesTokens(toCtxmemMessages(history), contextTokenEstimator)
	demand[ctxmem.LayerEpisodic] = s.compressor.SummaryTokens(conversationID)
	demand[ctxmem.LayerSemantic] = estimateOptionalText(semanticProbe) + ctxmem.EstimateText(semanticAckText)

	budget := ctxmem.Allocate(derived.Total, ctxmem.DefaultQuotas(derived.Total, demand))

	historyMessages := s.compressor.BuildContextMessages(
		ctx, conversationID, history, budget.Working, budget.Episodic, s.summaryChat)
	if len(historyMessages) == 0 {
		// 历史一条都装不下时不能只返回事实槽：那样会把当前问题弄丢。
		return nil
	}

	semanticText, omissions := ctxmem.RenderSlots(slots, budget.Semantic, contextTokenEstimator)

	out := make([]*schema.Message, 0, len(memoryMessages)+2+len(historyMessages))
	out = append(out, memoryMessages...)
	out = append(out, semanticContextMessages(semanticText)...)
	out = append(out, historyMessages...)

	logger.Info("上下文装配完成",
		logger.StringField("conversation_id", conversationID),
		logger.IntField("window_tokens", resolveContextWindow(bot)),
		logger.IntField("history_budget", budget.Total),
		logger.IntField("working_budget", budget.Working),
		logger.IntField("episodic_budget", budget.Episodic),
		logger.IntField("semantic_budget", budget.Semantic),
		logger.IntField("semantic_slots", len(slots)),
		logger.IntField("semantic_omitted", omissionCount(omissions)),
		logger.IntField("history_messages", len(historyMessages)))

	return out
}

// resolveContextWindow 读取 Bot 配置的模型窗口；配置缺失或非法时回退默认值。
func resolveContextWindow(bot *botmodel.Bot) int {
	if bot == nil {
		return defaultContextWindowTokens
	}
	raw := strings.TrimSpace(bot.Config["context_window"])
	if raw == "" {
		raw = strings.TrimSpace(bot.Config["context_window_tokens"])
	}
	if raw == "" {
		return defaultContextWindowTokens
	}
	window, err := strconv.Atoi(raw)
	if err != nil || window < minContextWindowTokens {
		logger.Warn("Bot 的 context_window 配置非法，使用默认窗口",
			logger.StringField("bot_id", bot.ID),
			logger.StringField("raw", raw))
		return defaultContextWindowTokens
	}
	return window
}

// syncConversationSlots 抽取当前用户消息里的事实并落库，返回本会话目前的全部槽位。
//
// 只抽取「当前这一条」，不回溯历史：每条消息在到达时已经抽取过一次，
// 每轮重扫 500 条会让一次对话产生 O(n²) 次写库。
func (s *botServiceImpl) syncConversationSlots(
	ctx context.Context, userMsg *botmodel.Message,
) []ctxmem.Slot {
	if s.repo == nil || userMsg == nil {
		return nil
	}

	// 轮次是冲突消解的全序键：没有它就无法判断 40 轮的改口是否晚于 3 轮的值。
	// 查询失败时宁可不写，也不要用错轮次把正确的旧值作废。
	turnIndex, err := s.repo.CountUserTurns(ctx, userMsg.ConversationID)
	if err != nil {
		logger.Warn("统计用户轮次失败，跳过事实槽抽取",
			logger.StringField("conversation_id", userMsg.ConversationID),
			logger.ErrorField(err))
	} else {
		extracted := ctxmem.ExtractSlots(ctxmem.SourceMessage{
			ID:        userMsg.ID,
			Role:      userMsg.Role,
			Content:   userMsg.Content,
			TurnIndex: turnIndex,
		})
		for _, slot := range extracted {
			if err := s.repo.SaveConversationSlot(ctx, modelSlot(userMsg.ConversationID, slot)); err != nil {
				logger.Warn("保存会话事实槽失败",
					logger.StringField("conversation_id", userMsg.ConversationID),
					logger.StringField("key", slot.Key),
					logger.ErrorField(err))
			}
		}
	}

	rows, err := s.repo.ListConversationSlots(ctx, userMsg.ConversationID)
	if err != nil {
		logger.Warn("读取会话事实槽失败",
			logger.StringField("conversation_id", userMsg.ConversationID),
			logger.ErrorField(err))
		return nil
	}
	slots := make([]ctxmem.Slot, 0, len(rows))
	for _, row := range rows {
		if row == nil {
			continue
		}
		slots = append(slots, ctxmem.Slot{
			ID:             row.ID,
			ConversationID: row.ConversationID,
			Key:            row.Key,
			Value:          row.Value,
			Kind:           row.Kind,
			SourceMsgID:    row.SourceMsgID,
			SourceRole:     row.SourceRole,
			TurnIndex:      row.TurnIndex,
			SupersededBy:   row.SupersededBy,
			Confidence:     row.Confidence,
			CreatedAt:      row.CreatedAt,
		})
	}
	return slots
}

func modelSlot(conversationID string, slot ctxmem.Slot) *botmodel.ConversationSlot {
	return &botmodel.ConversationSlot{
		ConversationID: conversationID,
		Key:            slot.Key,
		Value:          slot.Value,
		Kind:           slot.Kind,
		SourceMsgID:    slot.SourceMsgID,
		SourceRole:     slot.SourceRole,
		TurnIndex:      slot.TurnIndex,
		Confidence:     slot.Confidence,
	}
}

// semanticContextMessages 把事实槽包成一轮对话，保持 user/assistant 交替。
func semanticContextMessages(text string) []*schema.Message {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	return []*schema.Message{
		schema.UserMessage(text + "\n（以上是背景事实，无需逐条复述）"),
		schema.AssistantMessage(semanticAckText, nil),
	}
}

// estimateOptionalText 空文本返回 0。
//
// DefaultEstimator.Estimate 会给每条消息加固定开销，对「这一层没有内容」
// 也返回 50，直接拿来算 Demand 会让未触发的层凭空占预算。
func estimateOptionalText(text string) int {
	if strings.TrimSpace(text) == "" {
		return 0
	}
	return ctxmem.EstimateText(text)
}

func omissionCount(omissions []ctxmem.Omission) int {
	total := 0
	for _, omission := range omissions {
		total += omission.Count
	}
	return total
}
