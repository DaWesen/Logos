package service

import (
	"context"
	"testing"

	botmodel "Logos/internal/service/ai/bot/model"
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

// stubBillingService 记录计费调用是否发生以及调用时 ctx 的状态。
type stubBillingService struct {
	calls    int
	ctxErr   error
	tokens   int
	metadata map[string]string
}

func (s *stubBillingService) ConsumeModelCall(ctx context.Context, userID, provider, modelName string, tokenCount int, metadata map[string]string) (float64, error) {
	s.calls++
	s.ctxErr = ctx.Err()
	s.tokens = tokenCount
	s.metadata = metadata
	return 0.001, nil
}

// 请求被客户端取消（断开连接/切换会话）时，已经产生的 token 仍然必须计费。
func TestDoBilling_BillsEvenIfRequestContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	stub := &stubBillingService{}
	svc := &botServiceImpl{billingService: stub}
	bot := &botmodel.Bot{ID: "bot-1", Provider: "openai", Model: "deepseek-chat"}

	cost := svc.doBilling(ctx, "user-1", bot, 10, 20, eino.UsageSourceEstimate)

	if stub.calls != 1 {
		t.Fatalf("billing calls = %d, want 1", stub.calls)
	}
	if stub.ctxErr != nil {
		t.Errorf("billing ctx err = %v, want nil (计费不应随请求取消而中断)", stub.ctxErr)
	}
	if stub.tokens != 30 {
		t.Errorf("tokens = %d, want 30", stub.tokens)
	}
	if stub.metadata["token_source"] != eino.UsageSourceEstimate {
		t.Errorf("token_source = %q, want %q", stub.metadata["token_source"], eino.UsageSourceEstimate)
	}
	if cost != 0.001 {
		t.Errorf("cost = %v, want 0.001", cost)
	}
}
