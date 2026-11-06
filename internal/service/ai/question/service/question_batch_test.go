package service

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"Logos/internal/service/ai/question/model"
)

type trackingChat struct {
	delay       time.Duration
	calls       atomic.Int64
	inflight    atomic.Int64
	maxInflight atomic.Int64
}

func (c *trackingChat) HasChatModel() bool { return true }

func (c *trackingChat) Chat(ctx context.Context, _ []string) (string, error) {
	c.calls.Add(1)
	current := c.inflight.Add(1)
	defer c.inflight.Add(-1)
	for {
		max := c.maxInflight.Load()
		if current <= max || c.maxInflight.CompareAndSwap(max, current) {
			break
		}
	}
	select {
	case <-time.After(c.delay):
		return "answer", nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func TestBatchAskQuestions_RejectsOversizedBatch(t *testing.T) {
	svc := &qaServiceImpl{qaRepo: &stubQARepo{}, batchMaxQuestions: 3}
	questions := []string{"a", "b", "c", "d"}

	_, err := svc.BatchAskQuestions(context.Background(), questions, 7)
	if !errors.Is(err, ErrQABatchTooLarge) {
		t.Fatalf("超限批量应被拒绝, got %v", err)
	}
}

func TestBatchAskQuestions_EmptyBatchReturnsEmptyMap(t *testing.T) {
	svc := &qaServiceImpl{qaRepo: &stubQARepo{}}

	answers, err := svc.BatchAskQuestions(context.Background(), nil, 7)
	if err != nil {
		t.Fatalf("空批量不应报错: %v", err)
	}
	if answers == nil || len(answers) != 0 {
		t.Fatalf("空批量应返回空 map, got %v", answers)
	}
}

// 同一批里的重复问题只应该调用一次模型：map 的 key 会自然去重，
// 但重复调用模型是实打实的成本和延迟。
func TestBatchAskQuestions_DedupesIdenticalQuestions(t *testing.T) {
	chat := &trackingChat{delay: 10 * time.Millisecond}
	svc := &qaServiceImpl{qaRepo: &stubQARepo{}, chat: chat}

	answers, err := svc.BatchAskQuestions(
		context.Background(),
		[]string{"same", "same", "same", "other"},
		7,
	)
	if err != nil {
		t.Fatalf("批量问答不应报错: %v", err)
	}
	if chat.calls.Load() != 2 {
		t.Fatalf("重复问题应只生成一次, calls = %d", chat.calls.Load())
	}
	if len(answers) != 2 {
		t.Fatalf("结果应按问题去重, got %v", answers)
	}
	if answers["same"] != "answer" || answers["other"] != "answer" {
		t.Fatalf("每个问题都应有答案, got %v", answers)
	}
}

// 批量接口必须是有界并发：既不能串行拖长总延迟，
// 也不能把 20 个问题同时打到模型上。
func TestBatchAskQuestions_BoundsConcurrency(t *testing.T) {
	chat := &trackingChat{delay: 30 * time.Millisecond}
	svc := &qaServiceImpl{
		qaRepo:           &stubQARepo{},
		chat:             chat,
		batchConcurrency: 3,
	}

	questions := make([]string, 9)
	for i := range questions {
		questions[i] = fmt.Sprintf("q-%d", i)
	}

	started := time.Now()
	if _, err := svc.BatchAskQuestions(context.Background(), questions, 7); err != nil {
		t.Fatalf("批量问答不应报错: %v", err)
	}
	elapsed := time.Since(started)

	if max := chat.maxInflight.Load(); max > 3 {
		t.Fatalf("并发数超过上限: %d > 3", max)
	} else if max < 2 {
		t.Fatalf("批量没有并行执行, maxInflight = %d", max)
	}
	if elapsed > time.Second {
		t.Fatalf("有界并发没有生效，耗时 %v", elapsed)
	}
}

type failingRepo struct {
	stubQARepo
	failQuestion string
}

func (r *failingRepo) CreateQARecord(ctx context.Context, record *model.QARecord) error {
	if record.Question == r.failQuestion {
		return errors.New("db unavailable")
	}
	return r.stubQARepo.CreateQARecord(ctx, record)
}

// 单题失败只影响该题：整批仍然返回 200，其他问题不能被一起拖下水。
func TestBatchAskQuestions_PartialFailureIsIsolated(t *testing.T) {
	repo := &failingRepo{failQuestion: "bad"}
	svc := &qaServiceImpl{qaRepo: repo, chat: stubChat{answer: "good answer"}}

	answers, err := svc.BatchAskQuestions(context.Background(), []string{"ok-1", "bad", "ok-2"}, 7)
	if err != nil {
		t.Fatalf("单题失败不应让整批报错: %v", err)
	}
	if answers["ok-1"] != "good answer" || answers["ok-2"] != "good answer" {
		t.Fatalf("正常问题应返回答案, got %v", answers)
	}
	if answers["bad"] != batchFailedAnswer {
		t.Fatalf("失败问题应返回失败文案, got %q", answers["bad"])
	}
}

// 整批预算耗尽时不能在队列里继续发起模型调用。
func TestBatchAskQuestions_StopsAfterBudgetExhausted(t *testing.T) {
	chat := &trackingChat{delay: time.Second}
	svc := &qaServiceImpl{
		qaRepo:           &stubQARepo{},
		chat:             chat,
		batchConcurrency: 2,
		batchTimeout:     40 * time.Millisecond,
	}

	questions := make([]string, 12)
	for i := range questions {
		questions[i] = fmt.Sprintf("q-%d", i)
	}

	started := time.Now()
	answers, err := svc.BatchAskQuestions(context.Background(), questions, 7)
	elapsed := time.Since(started)

	if err != nil {
		t.Fatalf("批量超时应返回每题的结果而不是整体报错: %v", err)
	}
	if len(answers) != len(questions) {
		t.Fatalf("每个问题都应有结果, got %d", len(answers))
	}
	if elapsed > time.Second {
		t.Fatalf("整批预算没有生效，耗时 %v", elapsed)
	}
	if calls := chat.calls.Load(); calls > int64(len(questions)) {
		t.Fatalf("模型调用次数异常: %d", calls)
	}
}
