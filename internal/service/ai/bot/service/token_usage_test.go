package service

import (
	"context"
	"testing"

	"Logos/pkg/eino"

	"github.com/cloudwego/eino/schema"
)

func TestTokenUsage_PrefersEinoUsage(t *testing.T) {
	ctx, recorder := eino.WithUsageRecorder(context.Background())
	eino.RecordUsageFrom(ctx, &schema.Message{
		Role: schema.Assistant,
		ResponseMeta: &schema.ResponseMeta{
			Usage: &schema.TokenUsage{PromptTokens: 120, CompletionTokens: 30, TotalTokens: 150},
		},
	})

	in, out, source := tokenUsage(recorder, "问题", "答案")
	if source != eino.UsageSourceEino {
		t.Fatalf("source = %q, want %q", source, eino.UsageSourceEino)
	}
	if in != 120 || out != 30 {
		t.Errorf("tokens = (%d, %d), want (120, 30)", in, out)
	}
}

func TestTokenUsage_FallsBackToEstimate(t *testing.T) {
	_, recorder := eino.WithUsageRecorder(context.Background())

	in, out, source := tokenUsage(recorder, "hello world", "hi")
	if source != eino.UsageSourceEstimate {
		t.Fatalf("source = %q, want %q", source, eino.UsageSourceEstimate)
	}
	if in != estimateTokenCount("hello world") || out != estimateTokenCount("hi") {
		t.Errorf("tokens = (%d, %d), want estimate", in, out)
	}
}
