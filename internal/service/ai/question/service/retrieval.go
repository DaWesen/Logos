package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"Logos/pkg/logger"
	"Logos/pkg/obs"
)

const (
	// defaultAnswerBudget 是一次问答从检索到模型生成的端到端预算。
	// 没有它，一个挂住的模型调用会一直占着 gRPC 连接和 Kafka 消费协程，
	// 消费端甚至可能因为超过 max poll interval 被踢出消费组。
	defaultAnswerBudget = 20 * time.Second
	// defaultRetrievalBudget 是单个检索依赖的预算。检索只是“锦上添花”，
	// 任何一个依赖变慢都不应该拖垮整次回答。
	defaultRetrievalBudget = 800 * time.Millisecond
	// defaultPersistBudget 用于答案生成后的落库：此时预算可能已经耗尽，
	// 但一次已经生成的正常回答不应该因为请求超时而丢掉。
	defaultPersistBudget = 3 * time.Second

	// contextItemMaxRunes 限制单条参考信息长度，避免一篇长文挤掉其他来源。
	contextItemMaxRunes = 1200
	// contextBudgetRunes 是拼进 prompt 的参考信息总预算（按 rune 计），
	// 用来约束 token 成本和上下文窗口，而不是把检索结果无脑全塞进去。
	contextBudgetRunes = 6000
	// contextMinUsefulRunes 小于这个长度的剩余空间不再塞半句话。
	contextMinUsefulRunes = 80
)

// retrieval 描述一个可选的背景信息来源。
// 用统一的描述符而不是三段复制粘贴的 if，新增来源时只需要加一行。
type retrieval struct {
	source string
	fetch  func(context.Context) ([]string, error)
}

type retrievalOutcome struct {
	index    int
	source   string
	items    []string
	err      error
	duration time.Duration
}

// contextStats 记录参考信息在拼 prompt 时的处置结果。
type contextStats struct {
	kept       int
	duplicated int
	truncated  int
	dropped    int
}

// retrievals 按固定顺序返回当前配置可用的检索来源。
// 固定顺序保证同一份检索结果拼出的 prompt 是稳定的，便于排查与复现。
func (s *qaServiceImpl) retrievals(content string) []retrieval {
	retrievals := make([]retrieval, 0, 3)
	if s.knowledgeService != nil {
		retrievals = append(retrievals, retrieval{
			source: "knowledge_graph",
			fetch: func(ctx context.Context) ([]string, error) {
				return s.knowledgeService.SearchEntities(ctx, content)
			},
		})
	}
	if s.searchService != nil {
		retrievals = append(retrievals, retrieval{
			source: "search",
			fetch: func(ctx context.Context) ([]string, error) {
				return s.searchService.Search(ctx, content, "document")
			},
		})
	}
	if s.vectorService != nil {
		retrievals = append(retrievals, retrieval{
			source: "vector",
			fetch: func(ctx context.Context) ([]string, error) {
				return s.vectorService.SearchSimilar(ctx, content, 5)
			},
		})
	}
	return retrievals
}

// gatherBackground 并行执行所有检索，并把失败降级成“没有这条证据”。
//
// 两个关键点：
//  1. 等待方不依赖被调方遵守 ctx。即使某个 SDK 忽略取消，主流程也只会
//     等到整体预算耗尽，而不是被一个坏依赖永久拖住。
//  2. 每个来源单独计时、单独记录结果，这样“检索一直返回空”能在指标上
//     看见，而不是悄悄退化成没有依据的回答。
func (s *qaServiceImpl) gatherBackground(ctx context.Context, content string) ([]string, []string) {
	retrievals := s.retrievals(content)
	if len(retrievals) == 0 {
		return nil, nil
	}

	outcomes := make(chan retrievalOutcome, len(retrievals))
	for index, r := range retrievals {
		go func(index int, r retrieval) {
			started := time.Now()
			sourceCtx, cancel := context.WithTimeout(ctx, s.retrievalBudget())
			defer cancel()
			items, err := r.fetch(sourceCtx)
			// 缓冲通道长度等于来源数，主流程提前返回时这里也不会阻塞泄漏。
			outcomes <- retrievalOutcome{
				index:    index,
				source:   r.source,
				items:    items,
				err:      err,
				duration: time.Since(started),
			}
		}(index, r)
	}

	collected := make([]*retrievalOutcome, len(retrievals))
	received := 0
collect:
	for received < len(retrievals) {
		select {
		case out := <-outcomes:
			outcome := out
			collected[out.index] = &outcome
			received++
		case <-ctx.Done():
			// 整体预算先到：已经拿到的证据仍然可用，没回来的按取消处理。
			break collect
		}
	}
	for index := range collected {
		if collected[index] == nil {
			collected[index] = &retrievalOutcome{
				index:  index,
				source: retrievals[index].source,
				err:    ctx.Err(),
			}
		}
	}

	items := make([]string, 0, len(retrievals))
	sources := make([]string, 0, len(retrievals))
	for index, out := range collected {
		if out == nil {
			continue
		}
		result := classifyRetrieval(out.err, out.items)
		obs.RecordQARetrieval(ctx, out.source, result, out.duration.Seconds())

		if out.err != nil {
			logger.Warn("检索来源失败，降级为无该来源证据",
				logger.StringField("source", out.source),
				logger.StringField("result", result),
				logger.ErrorField(out.err))
			continue
		}
		if len(out.items) == 0 {
			continue
		}
		logger.Info("检索来源命中",
			logger.StringField("source", out.source),
			logger.IntField("count", len(out.items)))
		items = append(items, out.items...)
		sources = append(sources, retrievals[index].source)
	}
	return items, sources
}

func classifyRetrieval(err error, items []string) string {
	switch {
	case err == nil && len(items) > 0:
		return "ok"
	case err == nil:
		return "empty"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	default:
		return "error"
	}
}

// assembleContext 对检索结果做去重、单条截断和总量裁剪。
// 返回值里的 stats 只用于指标和日志，不参与回答逻辑。
func assembleContext(items []string) ([]string, contextStats) {
	stats := contextStats{}
	kept := make([]string, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	used := 0

	for _, raw := range items {
		item := strings.Join(strings.Fields(raw), " ")
		if item == "" {
			stats.dropped++
			continue
		}
		if _, ok := seen[item]; ok {
			stats.duplicated++
			continue
		}
		seen[item] = struct{}{}

		remaining := contextBudgetRunes - used
		if remaining <= 0 {
			stats.dropped++
			continue
		}
		limit := contextItemMaxRunes
		if remaining < limit {
			limit = remaining
		}
		if limit < contextMinUsefulRunes {
			stats.dropped++
			continue
		}
		trimmed := truncateRunes(item, limit)
		if trimmed != item {
			stats.truncated++
		}
		kept = append(kept, trimmed)
		used += utf8.RuneCountInString(trimmed)
	}

	stats.kept = len(kept)
	return kept, stats
}

func truncateRunes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit]) + "…"
}

// answerBudget / retrievalBudget / persistBudget 允许测试注入更短的预算，
// 生产路径使用默认值。
func (s *qaServiceImpl) answerBudget() time.Duration {
	if s.answerTimeout > 0 {
		return s.answerTimeout
	}
	return defaultAnswerBudget
}

func (s *qaServiceImpl) retrievalBudget() time.Duration {
	if s.retrievalTimeout > 0 {
		return s.retrievalTimeout
	}
	return defaultRetrievalBudget
}

func (s *qaServiceImpl) persistBudget() time.Duration {
	if s.persistTimeout > 0 {
		return s.persistTimeout
	}
	return defaultPersistBudget
}

// persistContext 从请求上下文派生一个只保留 value、不继承取消的落库上下文。
//
// 模型调用可能刚好在预算边界上返回：答案已经拿到，但请求 ctx 已经过期。
// 这时直接复用 ctx 会让一次成功的生成变成“answered but failed to record”。
func (s *qaServiceImpl) persistContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), s.persistBudget())
}

func (s *qaServiceImpl) buildPrompt(content string, background []string) []string {
	prompt := "You are a helpful AI assistant. Please answer the user's question based on the following information.\n\n"
	if len(background) > 0 {
		prompt += "Reference information:\n"
		for _, info := range background {
			prompt += "- " + info + "\n"
		}
		prompt += "\n"
	}
	prompt += "User question: " + content

	if len(background) == 0 {
		// 没有证据时显式告诉模型不要编造，比让它自由发挥更可控。
		prompt = "You are a helpful AI assistant. No reference information was retrieved for this question. " +
			"If you are not sure, say so instead of inventing facts.\n\nUser question: " + content
	}

	return []string{
		"You are a helpful AI assistant. Please answer the user's question based on the provided reference information.",
		prompt,
	}
}

func contextStatsSummary(stats contextStats) string {
	return fmt.Sprintf("kept=%d duplicated=%d truncated=%d dropped=%d",
		stats.kept, stats.duplicated, stats.truncated, stats.dropped)
}
