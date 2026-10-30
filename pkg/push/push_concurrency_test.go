package push

import (
	"context"
	"sync"
	"testing"
	"time"
)

// fakeProvider 记录并发峰值与调用次数，并可返回固定结果。
type fakeProvider struct {
	name  string
	delay time.Duration
	reply PushResult
	err   error

	mu        sync.Mutex
	active    int
	maxActive int
	calls     int
	tokens    []string
}

func (f *fakeProvider) Name() string { return f.name }

func (f *fakeProvider) Send(_ context.Context, msg *PushMessage) (*PushResult, error) {
	f.mu.Lock()
	f.active++
	if f.active > f.maxActive {
		f.maxActive = f.active
	}
	f.calls++
	f.tokens = append(f.tokens, msg.Token)
	f.mu.Unlock()

	if f.delay > 0 {
		time.Sleep(f.delay)
	}

	f.mu.Lock()
	f.active--
	f.mu.Unlock()

	result := f.reply
	result.UserID = msg.UserID
	return &result, f.err
}

func (f *fakeProvider) snapshot() (calls, maxActive int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls, f.maxActive
}

func newTestManager() *PushManager {
	return &PushManager{
		providers:          make(map[Platform]PushProvider),
		tokens:             make(map[string][]*PushToken),
		sendConcurrency:    DefaultSendConcurrency,
		perUserConcurrency: DefaultPerUserConcurrency,
	}
}

func androidToken(userID, deviceID string) *PushToken {
	return &PushToken{UserID: userID, DeviceID: deviceID, Platform: PlatformAndroid, Token: "tok-" + deviceID}
}

func TestSendToUser_DevicesRunConcurrentlyWithCap(t *testing.T) {
	m := newTestManager()
	m.SetPerUserConcurrency(3)

	provider := &fakeProvider{name: "android", delay: 25 * time.Millisecond, reply: PushResult{Success: true}}
	m.RegisterProvider(PlatformAndroid, provider)

	const devices = 9
	for i := 0; i < devices; i++ {
		m.RegisterToken(androidToken("u1", deviceID(i)))
	}

	results := m.SendToUser(context.Background(), "u1", "t", "b", nil)
	if len(results) != devices {
		t.Fatalf("结果数应与设备数一致，got %d want %d", len(results), devices)
	}
	for i, r := range results {
		if !r.Success {
			t.Fatalf("第 %d 个设备应发送成功: %+v", i, r)
		}
	}

	calls, maxActive := provider.snapshot()
	if calls != devices {
		t.Fatalf("每个设备都应发送一次，got %d", calls)
	}
	if maxActive > 3 {
		t.Fatalf("单用户并发不得超过 3，实测峰值 %d", maxActive)
	}
	if maxActive < 2 {
		t.Fatalf("设备之间应并发发送，实测峰值 %d（疑似退化为串行）", maxActive)
	}
}

func TestSendToUser_PermanentFailureEvictsToken(t *testing.T) {
	m := newTestManager()

	ios := &fakeProvider{name: "apns", reply: PushResult{Success: false, StatusCode: 410, Reason: "Unregistered"}}
	web := &fakeProvider{name: "web", reply: PushResult{Success: true}}
	m.RegisterProvider(PlatformIOS, ios)
	m.RegisterProvider(PlatformWeb, web)

	m.RegisterToken(&PushToken{UserID: "u2", DeviceID: "iphone", Platform: PlatformIOS, Token: "dead"})
	m.RegisterToken(&PushToken{UserID: "u2", DeviceID: "browser", Platform: PlatformWeb, Token: "alive"})

	results := m.SendToUser(context.Background(), "u2", "t", "b", nil)
	if len(results) != 2 {
		t.Fatalf("应返回两个设备的结果，got %d", len(results))
	}

	var dead *PushResult
	for i := range results {
		if !results[i].Success {
			dead = &results[i]
		}
	}
	if dead == nil {
		t.Fatal("应有一个失败结果")
	}
	if dead.Failure.Kind != PushFailurePermanent || !dead.Failure.TokenInvalid {
		t.Fatalf("410 应判定为永久失效，实际 %+v", dead.Failure)
	}

	// 失效 token 必须被注销，有效 token 保留
	remaining := m.GetUserTokens("u2")
	if len(remaining) != 1 || remaining[0].DeviceID != "browser" {
		t.Fatalf("应只剩 browser 令牌，实际 %+v", remaining)
	}
}

func TestSendToUser_RetryableFailureKeepsToken(t *testing.T) {
	m := newTestManager()

	provider := &fakeProvider{name: "fcm", reply: PushResult{Success: false, StatusCode: 503, Reason: "Unavailable"}}
	m.RegisterProvider(PlatformAndroid, provider)
	m.RegisterToken(androidToken("u3", "d1"))

	results := m.SendToUser(context.Background(), "u3", "t", "b", nil)
	if len(results) != 1 || results[0].Failure.Kind != PushFailureRetryable {
		t.Fatalf("503 应判定为可重试，实际 %+v", results)
	}
	if got := m.GetUserTokens("u3"); len(got) != 1 {
		t.Fatalf("可重试失败不得注销令牌，实际 %d 个", len(got))
	}
}

func TestSendToUser_NoProviderSkipped(t *testing.T) {
	m := newTestManager()
	m.RegisterToken(&PushToken{UserID: "u4", DeviceID: "d", Platform: PlatformDesktop, Token: "tok"})

	results := m.SendToUser(context.Background(), "u4", "t", "b", nil)
	if len(results) != 1 {
		t.Fatalf("无 provider 也应返回一条结果，got %d", len(results))
	}
	if results[0].Success || results[0].Failure.Kind != PushFailureUnknown {
		t.Fatalf("无 provider 应为未知失败: %+v", results[0])
	}
	if got := m.GetUserTokens("u4"); len(got) != 1 {
		t.Fatal("无 provider 不应注销令牌")
	}
}

func TestSendToUser_CanceledContextSkipsProvider(t *testing.T) {
	m := newTestManager()
	provider := &fakeProvider{name: "android", reply: PushResult{Success: true}}
	m.RegisterProvider(PlatformAndroid, provider)
	m.RegisterToken(androidToken("u5", "d1"))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	results := m.SendToUser(ctx, "u5", "t", "b", nil)
	if len(results) != 1 {
		t.Fatalf("应返回一条被跳过结果，got %d", len(results))
	}
	if results[0].Success {
		t.Fatalf("已取消的 ctx 不应发送成功: %+v", results[0])
	}
	if calls, _ := provider.snapshot(); calls != 0 {
		t.Fatalf("ctx 已取消时不应调用 provider，实际调用 %d 次", calls)
	}
}

func TestSendToUsers_GlobalConcurrencyCapAndDedupe(t *testing.T) {
	m := newTestManager()
	m.SetSendConcurrency(3)

	provider := &fakeProvider{name: "android", delay: 20 * time.Millisecond, reply: PushResult{Success: true}}
	m.RegisterProvider(PlatformAndroid, provider)

	const users = 10
	for i := 0; i < users; i++ {
		m.RegisterToken(androidToken("user"+string(rune('a'+i)), "d1"))
	}

	// 名单带重复与空 ID：去重后应是 10 个用户
	userIDs := []string{"", "usera", "usera"}
	for i := 0; i < users; i++ {
		userIDs = append(userIDs, "user"+string(rune('a'+i)))
	}

	m.SendToUsers(context.Background(), userIDs, "t", "b", nil)

	calls, maxActive := provider.snapshot()
	if calls != users {
		t.Fatalf("去重后应对 %d 个用户各发送一次，实际 %d 次", users, calls)
	}
	if maxActive > 3 {
		t.Fatalf("跨用户并发不得超过 3，实测峰值 %d", maxActive)
	}
	if maxActive < 2 {
		t.Fatalf("用户之间应并发发送，实测峰值 %d", maxActive)
	}
}

func TestSendToUsers_EmptyAfterDedupe(t *testing.T) {
	m := newTestManager()
	provider := &fakeProvider{name: "android", reply: PushResult{Success: true}}
	m.RegisterProvider(PlatformAndroid, provider)

	m.SendToUsers(context.Background(), []string{"", ""}, "t", "b", nil)
	if calls, _ := provider.snapshot(); calls != 0 {
		t.Fatalf("空名单不应触发任何发送，实际 %d 次", calls)
	}
}

func TestUnregisterToken_RemovesEmptyUserEntry(t *testing.T) {
	m := newTestManager()
	m.RegisterToken(androidToken("u6", "d1"))
	m.UnregisterToken("u6", "d1")

	if got := m.GetUserTokens("u6"); len(got) != 0 {
		t.Fatalf("注销后不应有令牌，实际 %d", len(got))
	}
	// 空 key 必须被回收，否则用户维度记录永久泄漏
	m.mu.RLock()
	_, exists := m.tokens["u6"]
	m.mu.RUnlock()
	if exists {
		t.Fatal("用户令牌清空后应删除 map 中的用户 key")
	}
}

func deviceID(i int) string {
	return "device-" + string(rune('0'+i%10)) + "-" + string(rune('a'+i/10))
}
