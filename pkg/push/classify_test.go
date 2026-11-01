package push

import (
	"os"
	"testing"

	"Logos/pkg/logger"

	"go.uber.org/zap"
)

// TestMain 注入空日志：push 包会在失败路径打 Warn/Info，
// 默认 logger 依赖 ./config 的日志配置，单测环境下会直接失败。
func TestMain(m *testing.M) {
	logger.SetLogger(zap.NewNop())
	os.Exit(m.Run())
}

func TestClassifyPushFailure_APNS(t *testing.T) {
	cases := []struct {
		name         string
		status       int
		reason       string
		wantKind     PushFailureKind
		wantUnregist bool
	}{
		{"410 注销", 410, "", PushFailurePermanent, true},
		{"Unregistered", 400, "Unregistered", PushFailurePermanent, true},
		{"BadDeviceToken", 400, "BadDeviceToken", PushFailurePermanent, true},
		{"DeviceTokenNotForTopic", 400, "DeviceTokenNotForTopic", PushFailurePermanent, true},
		{"429 限流", 429, "", PushFailureRetryable, false},
		{"503 服务不可用", 503, "ServiceUnavailable", PushFailureRetryable, false},
		{"500 无 reason 也可重试", 500, "", PushFailureRetryable, false},
		{"PayloadTooLarge 永久但不注销", 413, "PayloadTooLarge", PushFailurePermanent, false},
		{"403 授权问题未知", 403, "MissingProviderToken", PushFailurePermanent, false},
		{"未知 reason + 200", 200, "", PushFailureUnknown, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ClassifyPushFailure(PlatformIOS, c.status, c.reason)
			if got.Kind != c.wantKind {
				t.Fatalf("kind=%s, want %s (status=%d reason=%q)", got.Kind, c.wantKind, c.status, c.reason)
			}
			if got.ShouldUnregister() != c.wantUnregist {
				t.Fatalf("ShouldUnregister=%v, want %v (status=%d reason=%q)",
					got.ShouldUnregister(), c.wantUnregist, c.status, c.reason)
			}
		})
	}
}

func TestClassifyPushFailure_FCM(t *testing.T) {
	cases := []struct {
		name         string
		status       int
		reason       string
		wantKind     PushFailureKind
		wantUnregist bool
	}{
		{"NotRegistered", 200, "NotRegistered", PushFailurePermanent, true},
		{"InvalidRegistration", 200, "InvalidRegistration", PushFailurePermanent, true},
		{"MismatchSenderId", 200, "MismatchSenderId", PushFailurePermanent, true},
		{"Unavailable 可重试", 200, "Unavailable", PushFailureRetryable, false},
		{"401 凭据问题不注销", 401, "", PushFailureUnknown, false},
		{"403 凭据问题不注销", 403, "", PushFailureUnknown, false},
		{"429 可重试", 429, "", PushFailureRetryable, false},
		{"400 请求问题永久", 400, "InvalidDataKey", PushFailurePermanent, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ClassifyPushFailure(PlatformAndroid, c.status, c.reason)
			if got.Kind != c.wantKind || got.ShouldUnregister() != c.wantUnregist {
				t.Fatalf("got kind=%s unregister=%v, want kind=%s unregister=%v",
					got.Kind, got.ShouldUnregister(), c.wantKind, c.wantUnregist)
			}
		})
	}
}

func TestPushPlatformLabel(t *testing.T) {
	cases := []struct {
		platform Platform
		want     string
	}{
		{PlatformIOS, "ios"},
		{PlatformAndroid, "android"},
		{PlatformWeb, "web"},
		{PlatformDesktop, "desktop"},
		{Platform("ios2"), "unknown"},
		{Platform("IOS"), "unknown"},
		{Platform(""), "unknown"},
	}
	for _, tc := range cases {
		if got := pushPlatformLabel(tc.platform); got != tc.want {
			t.Fatalf("pushPlatformLabel(%q) = %q, want %q", tc.platform, got, tc.want)
		}
	}
}

func TestClassifyPushFailure_WebPush(t *testing.T) {
	// RFC 8030：404 / 410 表示订阅已不存在，必须注销；429 / 5xx 可重试；
	// 其余 4xx 是请求级永久失败，但订阅本身可能仍然有效，不注销。
	if got := ClassifyPushFailure(PlatformWeb, 500, ""); got.Kind != PushFailureRetryable {
		t.Fatalf("5xx 应为可重试，实际 %s", got.Kind)
	}
	if got := ClassifyPushFailure(PlatformWeb, 429, ""); got.Kind != PushFailureRetryable {
		t.Fatalf("429 应为可重试，实际 %s", got.Kind)
	}
	for _, code := range []int{404, 410} {
		got := ClassifyPushFailure(PlatformWeb, code, "")
		if got.Kind != PushFailurePermanent || !got.ShouldUnregister() {
			t.Fatalf("%d 应为永久失败且注销订阅，实际 kind=%s unregister=%v",
				code, got.Kind, got.ShouldUnregister())
		}
	}
	if got := ClassifyPushFailure(PlatformWeb, 400, ""); got.Kind == PushFailureRetryable || got.ShouldUnregister() {
		t.Fatalf("400 属请求级永久失败但不注销，实际 kind=%s unregister=%v", got.Kind, got.ShouldUnregister())
	}
	if got := ClassifyPushFailure(PlatformWeb, 401, ""); got.Kind != PushFailureUnknown {
		t.Fatalf("401（VAPID 鉴权失败）应为未知，实际 %s", got.Kind)
	}
}

func TestClassifyPushFailure_ReasonTrim(t *testing.T) {
	got := ClassifyPushFailure(PlatformIOS, 400, "  Unregistered ")
	if !got.ShouldUnregister() {
		t.Fatalf("reason 首尾空白应被裁剪，实际 kind=%s", got.Kind)
	}
}
