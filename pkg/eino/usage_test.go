package eino

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func msgWithUsage(prompt, completion, total int) *schema.Message {
	return &schema.Message{
		Role: schema.Assistant,
		ResponseMeta: &schema.ResponseMeta{
			Usage: &schema.TokenUsage{
				PromptTokens:     prompt,
				CompletionTokens: completion,
				TotalTokens:      total,
			},
		},
	}
}

func TestUsageRecorder_AccumulatesAcrossCalls(t *testing.T) {
	ctx, recorder := WithUsageRecorder(context.Background())

	RecordUsageFrom(ctx, msgWithUsage(100, 20, 120))
	RecordUsageFrom(ctx, msgWithUsage(50, 10, 60))

	usage, calls := recorder.Snapshot()
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
	if usage.PromptTokens != 150 || usage.CompletionTokens != 30 || usage.TotalTokens != 180 {
		t.Errorf("usage = %+v, want prompt=150 completion=30 total=180", usage)
	}
}

func TestUsageRecorder_IgnoresMessagesWithoutUsage(t *testing.T) {
	ctx, recorder := WithUsageRecorder(context.Background())

	RecordUsageFrom(ctx, nil)
	RecordUsageFrom(ctx, &schema.Message{Role: schema.Assistant, Content: "hi"})
	RecordUsageFrom(ctx, &schema.Message{Role: schema.Assistant, ResponseMeta: &schema.ResponseMeta{}})

	if usage, calls := recorder.Snapshot(); calls != 0 || usage.TotalTokens != 0 {
		t.Errorf("应忽略无 usage 的消息，实际 calls=%d usage=%+v", calls, usage)
	}
}

func TestUsageRecorder_AbsentFromContext(t *testing.T) {
	// 没有挂载采集器时不应 panic
	RecordUsageFrom(context.Background(), msgWithUsage(1, 1, 2))
}

func TestUsageRecorder_NilSnapshot(t *testing.T) {
	var recorder *UsageRecorder
	usage, calls := recorder.Snapshot()
	if calls != 0 || usage.TotalTokens != 0 {
		t.Errorf("nil recorder snapshot = %+v, calls=%d", usage, calls)
	}
}
