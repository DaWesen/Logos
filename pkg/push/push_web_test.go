package push

import (
	"context"
	"errors"
	"testing"
)

// TestWebPushProviderSendIsExplicitFailure 验证未实现的 Web Push 不会伪装成功。
//
// 原实现返回 Success=true：调用方认为已送达、不再重试，设备却一条通知都收不到。
// 现在必须显式失败，且分类为 permanent（重试无意义）但**不可注销令牌**——
// 令牌未必失效，只是服务端没实现。
func TestWebPushProviderSendIsExplicitFailure(t *testing.T) {
	p := NewWebPushProvider(WebPushConfig{})
	res, err := p.Send(context.Background(), &PushMessage{
		UserID:   "u1",
		Platform: PlatformWeb,
		Token:    "web-token",
	})

	if !errors.Is(err, ErrWebPushNotImplemented) {
		t.Fatalf("应返回 ErrWebPushNotImplemented，实际 %v", err)
	}
	if res == nil {
		t.Fatal("失败时也必须返回结果，便于调用方对账")
	}
	if res.Success {
		t.Fatal("未实现通道不得报告成功")
	}
	if res.Failure.Kind != PushFailurePermanent {
		t.Fatalf("分类应为 permanent，实际 %q", res.Failure.Kind)
	}
	if res.Failure.TokenInvalid {
		t.Fatal("未实现通道不应把令牌标记为失效")
	}
	if res.Failure.ShouldUnregister() {
		t.Fatal("未实现通道绝不能注销用户令牌")
	}
}

// TestWebPushProviderName 守住 provider 名字：它被用作 providers map 的键，
// 改名会让已注册的 web 令牌在发送时找不到 provider（静默 skipped）。
func TestWebPushProviderName(t *testing.T) {
	if got := NewWebPushProvider(WebPushConfig{}).Name(); got != "webpush" {
		t.Fatalf("provider name = %q, want %q", got, "webpush")
	}
}
