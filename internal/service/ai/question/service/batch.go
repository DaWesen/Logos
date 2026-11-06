package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"Logos/pkg/logger"
	"Logos/pkg/obs"
)

const (
	// defaultBatchLimit 与单用户每分钟 30 次的限流对齐：
	// 一次批量请求最多消耗 20 次配额，剩下的留给用户正常追问。
	defaultBatchLimit = 20
	// defaultBatchWorkers 限制单次批量的并发模型调用数。
	// 无界并发会把一次批量请求放大成几十个同时在跑的模型调用，
	// 既打爆上游配额，也让失败时的重试风暴更难收敛。
	defaultBatchWorkers = 4
	// defaultBatchBudget 是整批请求的预算。单题有 20s 预算，
	// 但 20 题排队执行不能真的等到 100s，否则调用方早就超时了。
	defaultBatchBudget = 60 * time.Second

	batchTimeoutAnswer = "批量问答超时，请稍后重试"
	batchFailedAnswer  = "Failed to answer, please try again later"
)

// ErrQABatchTooLarge 表示一次提交的问题数超过上限。
// 明确拒绝比静默截断更安全：调用方至少知道自己少问了哪些问题。
var ErrQABatchTooLarge = errors.New("qa: too many questions in one batch")

func (s *qaServiceImpl) batchLimit() int {
	if s.batchMaxQuestions > 0 {
		return s.batchMaxQuestions
	}
	return defaultBatchLimit
}

func (s *qaServiceImpl) batchWorkers() int {
	if s.batchConcurrency > 0 {
		return s.batchConcurrency
	}
	return defaultBatchWorkers
}

func (s *qaServiceImpl) batchBudget() time.Duration {
	if s.batchTimeout > 0 {
		return s.batchTimeout
	}
	return defaultBatchBudget
}

// BatchAskQuestions 用有界 worker pool 处理批量问题。
//
// 旧实现是 for 循环串行调用：20 个问题就是 20 次模型往返，
// 而且没有任何数量上限。这里做三件事：
//  1. 超过上限直接拒绝，避免一次请求放大成几十次模型调用；
//  2. 同一批里的重复问题只生成一次（map 的 key 本来就会去重，
//     但重复调用模型是实打实的成本）；
//  3. 并发数固定，单题失败只影响该题，不拖垮整批。
func (s *qaServiceImpl) BatchAskQuestions(ctx context.Context, questions []string, userID int64) (map[string]string, error) {
	logger.Info("Processing batch questions",
		logger.IntField("count", len(questions)),
		logger.Int64Field("user_id", userID))

	if len(questions) == 0 {
		return map[string]string{}, nil
	}
	if limit := s.batchLimit(); len(questions) > limit {
		obs.RecordQABatch(ctx, "rejected")
		return nil, fmt.Errorf("%w: %d > %d", ErrQABatchTooLarge, len(questions), limit)
	}

	unique := dedupeQuestions(questions)
	if len(unique) < len(questions) {
		logger.Info("批量问题已去重",
			logger.IntField("submitted", len(questions)),
			logger.IntField("unique", len(unique)))
	}

	ctx, cancel := context.WithTimeout(ctx, s.batchBudget())
	defer cancel()

	answers := make([]string, len(unique))
	jobs := make(chan int)
	var failed atomic.Int64
	var timedOut atomic.Int64

	workers := s.batchWorkers()
	if workers > len(unique) {
		workers = len(unique)
	}

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				// 已排队但整批预算已经用完：不再发起新的模型调用。
				if ctx.Err() != nil {
					answers[index] = batchTimeoutAnswer
					timedOut.Add(1)
					continue
				}
				answer, _, _, _, _, _, err := s.AskQuestion(ctx, unique[index], userID, nil, "")
				if err != nil {
					logger.Warn("Failed to process question",
						logger.StringField("question", unique[index]),
						logger.ErrorField(err))
					answers[index] = batchFailedAnswer
					failed.Add(1)
					continue
				}
				answers[index] = answer
			}
		}()
	}

	for index := range unique {
		jobs <- index
	}
	close(jobs)
	wg.Wait()

	result := make(map[string]string, len(questions))
	positions := make(map[string]int, len(unique))
	for index, question := range unique {
		positions[question] = index
	}
	for _, question := range questions {
		if index, ok := positions[question]; ok {
			result[question] = answers[index]
		}
	}

	switch failures := failed.Load() + timedOut.Load(); {
	case failures == 0:
		obs.RecordQABatch(ctx, "ok")
	case failures >= int64(len(unique)):
		obs.RecordQABatch(ctx, "failed")
	default:
		obs.RecordQABatch(ctx, "partial")
	}

	return result, nil
}

func dedupeQuestions(questions []string) []string {
	seen := make(map[string]struct{}, len(questions))
	unique := make([]string, 0, len(questions))
	for _, question := range questions {
		if _, ok := seen[question]; ok {
			continue
		}
		seen[question] = struct{}{}
		unique = append(unique, question)
	}
	return unique
}
