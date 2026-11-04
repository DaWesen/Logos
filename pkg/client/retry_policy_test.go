package client

import (
	"testing"

	"Logos/config"
)

// TestBuildRetryPolicy_ReadsConfig
// 配置里的 retry_safe_methods 必须真的被读取，否则又是一处
// 「配置写了但没人读」——这类问题在线上表现为「调了参数没反应」。
func TestBuildRetryPolicy_ReadsConfig(t *testing.T) {
	cfg := &config.Config{}
	cfg.GRPC.Client.RetrySafeMethods = []string{"/logos.custom.Svc/DoThing"}

	policy := buildRetryPolicy(cfg)
	if !policy.ShouldRetry("/logos.custom.Svc/DoThing") {
		t.Fatal("配置的追加方法未生效")
	}
	if policy.ShouldRetry("/logos.custom.Svc/OtherThing") {
		t.Fatal("未配置的方法不应允许重试")
	}
}

func TestBuildRetryPolicy_DoesNotAllowDangerousWrites(t *testing.T) {
	// 即使配置为空，默认白名单也不能放行非幂等写。
	cfg := &config.Config{}
	policy := buildRetryPolicy(cfg)
	for _, method := range []string{
		"/logos.chat.ChatService/SendMessage",
		"/logos.billing.BillingService/Deposit",
		"/logos.billing.BillingService/ConsumeModelCall",
	} {
		if policy.ShouldRetry(method) {
			t.Fatalf("默认配置不应允许重试非幂等写: %s", method)
		}
	}
}

func TestBuildRetryPolicy_CopiesSlice(t *testing.T) {
	cfg := &config.Config{}
	cfg.GRPC.Client.RetrySafeMethods = []string{"/a.B/C"}

	policy := buildRetryPolicy(cfg)
	cfg.GRPC.Client.RetrySafeMethods[0] = "/a.B/Changed"

	if !policy.ShouldRetry("/a.B/C") {
		t.Fatal("策略应持有配置切片的副本，避免调用方后续修改影响已建连接")
	}
}
