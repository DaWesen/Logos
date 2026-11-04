package governance

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestRetryPolicy_NonIdempotentWritesAreNotRetried(t *testing.T) {
	// 这些方法的副作用不能重复：重复发消息、重复充值、重复扣费。
	dangerous := []string{
		"/logos.chat.ChatService/SendMessage",
		"/logos.bot.BotService/SendBotMessage",
		"/logos.billing.BillingService/CreatePaymentOrder",
		"/logos.billing.BillingService/ConfirmPayment",
		"/logos.billing.BillingService/Deposit",
		"/logos.billing.BillingService/Withdraw",
		"/logos.billing.BillingService/ConsumeModelCall",
		"/logos.billing.BillingService/ConsumeEmbedding",
		"/logos.billing.BillingService/Refund",
		"/logos.contact.ContactService/AddFriend",
		"/logos.bot.BotService/CreateBot",
		"/logos.bot.BotService/DeleteBot",
	}

	policy := RetryPolicy{}
	for _, method := range dangerous {
		if policy.ShouldRetry(method) {
			t.Fatalf("非幂等写方法不应允许重试: %s", method)
		}
	}
}

func TestRetryPolicy_ReadOnlyMethodsAreRetried(t *testing.T) {
	safe := []string{
		"/logos.chat.ChatService/GetMessageHistory",
		"/logos.user.UserService/GetUserInfo",
		"/logos.billing.BillingService/GetAccount",
		"/logos.bot.BotService/GetBot",
		"/logos.monitoring.MonitoringService/QueryMetrics",
		"GetUserInfo",
	}
	policy := RetryPolicy{}
	for _, method := range safe {
		if !policy.ShouldRetry(method) {
			t.Fatalf("只读方法应允许重试: %s", method)
		}
	}
}

func TestRetryPolicy_EmptyMethodIsNotRetried(t *testing.T) {
	// 方法名缺失时保守处理：宁可不重试，也不要赌它是幂等的。
	if (RetryPolicy{}).ShouldRetry("") {
		t.Fatal("空方法名不应允许重试")
	}
}

func TestRetryPolicy_ExtraSafeMethods(t *testing.T) {
	policy := RetryPolicy{ExtraSafeMethods: []string{"/logos.custom.Svc/DoThing"}}
	if !policy.ShouldRetry("/logos.custom.Svc/DoThing") {
		t.Fatal("显式追加的方法应允许重试")
	}
	if policy.ShouldRetry("/logos.custom.Svc/DoOtherThing") {
		t.Fatal("未追加的方法不应允许重试")
	}
}

// TestRetryManager_ExecuteMethod_StopsNonIdempotentWrite
// 即使下游返回 Unavailable（典型可重试码），非幂等写也只能调用一次。
func TestRetryManager_ExecuteMethod_StopsNonIdempotentWrite(t *testing.T) {
	m := NewRetryManager(RetryConfig{
		MaxAttempts:    3,
		InitialDelay:   time.Millisecond,
		MaxDelay:       5 * time.Millisecond,
		RetryableCodes: []string{"UNAVAILABLE"},
	})

	calls := 0
	err := m.ExecuteMethod(context.Background(), "/logos.chat.ChatService/SendMessage", func() error {
		calls++
		return status.Error(codes.Unavailable, "down")
	})
	if err == nil {
		t.Fatal("应返回错误")
	}
	if calls != 1 {
		t.Fatalf("非幂等写只能调用一次，实际 %d 次", calls)
	}
}

func TestRetryManager_ExecuteMethod_RetriesReadOnly(t *testing.T) {
	m := NewRetryManager(RetryConfig{
		MaxAttempts:    3,
		InitialDelay:   time.Millisecond,
		MaxDelay:       5 * time.Millisecond,
		RetryableCodes: []string{"UNAVAILABLE"},
	})

	calls := 0
	err := m.ExecuteMethod(context.Background(), "/logos.user.UserService/GetUserInfo", func() error {
		calls++
		if calls < 3 {
			return status.Error(codes.Unavailable, "down")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("只读方法应重试到成功: %v", err)
	}
	if calls != 3 {
		t.Fatalf("应重试 3 次，实际 %d", calls)
	}
}

// TestRetryManager_HonorsConfiguredCodes
// 配置写了 retryable_codes，就必须真的生效，而不是硬编码三个码。
func TestRetryManager_HonorsConfiguredCodes(t *testing.T) {
	m := NewRetryManager(RetryConfig{
		MaxAttempts:    2,
		InitialDelay:   time.Millisecond,
		MaxDelay:       5 * time.Millisecond,
		RetryableCodes: []string{"ABORTED"},
	})

	calls := 0
	_ = m.Execute(context.Background(), func() error {
		calls++
		return status.Error(codes.Aborted, "aborted")
	})
	if calls != 2 {
		t.Fatalf("配置的 ABORTED 应可重试，实际调用 %d 次", calls)
	}

	// UNAVAILABLE 不在配置里，不应重试。
	calls = 0
	_ = m.Execute(context.Background(), func() error {
		calls++
		return status.Error(codes.Unavailable, "down")
	})
	if calls != 1 {
		t.Fatalf("未配置的 UNAVAILABLE 不应重试，实际调用 %d 次", calls)
	}
}

func TestRetryManager_EmptyCodesKeepSafeDefault(t *testing.T) {
	// 配置为空不能等于「完全不重试」：保留最小安全集合。
	m := NewRetryManager(RetryConfig{
		MaxAttempts:  2,
		InitialDelay: time.Millisecond,
		MaxDelay:     5 * time.Millisecond,
	})
	calls := 0
	_ = m.Execute(context.Background(), func() error {
		calls++
		return status.Error(codes.Unavailable, "down")
	})
	if calls != 2 {
		t.Fatalf("空配置应保留安全默认，实际调用 %d 次", calls)
	}
}

// TestSameCodeName_NormalizesGRPCSpelling
//
// 配置里通常写 UNAVAILABLE / RESOURCE_EXHAUSTED（gRPC 官方文档风格），
// 但 codes.Code.String() 返回 Unavailable / ResourceExhausted。
// 之前的 EqualFold 匹配不上，导致配置里的重试码全部静默失效——
// 只因为硬编码分支兜底，才看起来「能重试」。
func TestSameCodeName_NormalizesGRPCSpelling(t *testing.T) {
	cases := []struct {
		configured, actual string
		want               bool
	}{
		{"UNAVAILABLE", "Unavailable", true},
		{"RESOURCE_EXHAUSTED", "ResourceExhausted", true},
		{"DEADLINE_EXCEEDED", "DeadlineExceeded", true},
		{" unavailable ", "Unavailable", true},
		{"ABORTED", "Unavailable", false},
	}
	for _, c := range cases {
		if got := sameCodeName(c.configured, c.actual); got != c.want {
			t.Fatalf("sameCodeName(%q,%q) = %v, want %v",
				c.configured, c.actual, got, c.want)
		}
	}
}
