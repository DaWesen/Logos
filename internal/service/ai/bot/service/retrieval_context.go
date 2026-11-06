package service

import (
	"context"
	"time"

	"Logos/internal/bot/ctxmem"
	"Logos/pkg/logger"

	"github.com/cloudwego/eino/schema"
)

const (
	// retrievalTimeout 检索必须明显快于主模型调用，否则用户会先感到「卡了一下」。
	// 超过 200ms 就当作这一层不存在：它只是锦上添花，不能拖慢整轮回复。
	retrievalTimeout = 200 * time.Millisecond
	// retrievalIndexTTL 会话索引复用窗口。会话有新消息时指纹会变化，
	// 下一次检索会重建，所以 TTL 只负责清理长期不活跃的会话。
	retrievalIndexTTL = 10 * time.Minute
	// retrievalIndexMax 进程内最多缓存多少个会话的索引。
	retrievalIndexMax = 64
)

// retrievalCandidates 一次检索的候选集。
//
// 返回 hits 而不是渲染好的文本，是因为最终渲染要排除 Working 已保留的
// 消息：同一段原文在摘要、检索、Working 里重复出现，既浪费预算，
// 也会让模型误以为用户重复说过同一句话。
type retrievalCandidates struct {
	hits   []ctxmem.Hit
	skip   string
	detail string
}

// loadRetrievalCandidates 拉取足够长的历史并执行会话内检索。
//
// 任何失败都只返回 skip 原因，不向上抛错：调用方按「少一层」继续组装。
func (s *botServiceImpl) loadRetrievalCandidates(
	ctx context.Context, conversationID, query string, slots []ctxmem.Slot,
) retrievalCandidates {
	if s.repo == nil || s.indexCache == nil {
		return retrievalCandidates{skip: "unavailable"}
	}

	retrievalCtx, cancel := context.WithTimeout(ctx, retrievalTimeout)
	defer cancel()

	// 索引需要完整窗口，不能只取 Working 预算内的消息：
	// 用户问的「那个订单」很可能在第 3 轮，早已被挤出了 Working。
	history, err := s.fetchHistoryMessages(retrievalCtx, conversationID, 0)
	if err != nil {
		return retrievalCandidates{skip: skipReason(retrievalCtx, "fetch_error"), detail: err.Error()}
	}
	messages := toCtxmemMessages(history)
	if len(messages) == 0 {
		return retrievalCandidates{skip: "empty"}
	}

	fingerprint := ctxmem.Fingerprint(messages)
	index := s.indexCache.Get(conversationID, fingerprint)
	if index == nil {
		index = ctxmem.BuildIndex(messages)
		if err := retrievalCtx.Err(); err != nil {
			return retrievalCandidates{skip: skipReason(retrievalCtx, "timeout")}
		}
		s.indexCache.Put(conversationID, fingerprint, index)
	}

	hits := index.Search(query, slots)
	if err := retrievalCtx.Err(); err != nil {
		return retrievalCandidates{skip: skipReason(retrievalCtx, "timeout")}
	}
	return retrievalCandidates{hits: hits}
}

func skipReason(ctx context.Context, fallback string) string {
	if ctx.Err() == context.DeadlineExceeded {
		return "timeout"
	}
	return fallback
}

// retrievalContextMessages 把检索结果包成一轮对话。
func retrievalContextMessages(text string) []*schema.Message {
	if text == "" {
		return nil
	}
	return []*schema.Message{
		schema.UserMessage(text + "\n（以上是历史原文，回答时可直接引用，但不要声称是用户刚刚说的）"),
		schema.AssistantMessage("好的，我已找到相关历史原文。", nil),
	}
}

func logRetrievalSkip(conversationID string, candidates retrievalCandidates) {
	if candidates.skip == "" {
		return
	}
	logger.Warn("会话内检索跳过",
		logger.StringField("conversation_id", conversationID),
		logger.StringField("reason", candidates.skip),
		logger.StringField("detail", candidates.detail))
}
