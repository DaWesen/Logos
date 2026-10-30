package gateway

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"Logos/internal/service/messaging/types"
	"Logos/pkg/client"
)

type fakeUserSource struct {
	mu    sync.Mutex
	calls int
	users map[int64]*client.UserInfo
	err   error
}

func (f *fakeUserSource) GetUserInfo(_ context.Context, userID int64) (*client.UserInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.users[userID], nil
}

func (f *fakeUserSource) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func newTestEnricher(src UserInfoSource, now *time.Time) *SenderEnricher {
	e := NewSenderEnricher(src)
	e.now = func() time.Time { return *now }
	return e
}

// TestSenderEnricherCachesWithinTTL 同一发送者的连续消息只查一次用户服务。
func TestSenderEnricherCachesWithinTTL(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	src := &fakeUserSource{users: map[int64]*client.UserInfo{
		1001: {UserID: 1001, Username: "alice", Avatar: "a.png"},
	}}
	e := newTestEnricher(src, &now)

	first := &types.MessageEvent{SenderID: "1001"}
	e.Enrich(context.Background(), first)
	if first.SenderName != "alice" || first.SenderAvatar != "a.png" {
		t.Fatalf("first enrich = %q/%q, want alice/a.png", first.SenderName, first.SenderAvatar)
	}

	second := &types.MessageEvent{SenderID: "1001"}
	e.Enrich(context.Background(), second)
	if second.SenderName != "alice" {
		t.Fatalf("second enrich name = %q, want alice", second.SenderName)
	}
	if calls := src.callCount(); calls != 1 {
		t.Fatalf("user service calls = %d, want 1 (second message should hit cache)", calls)
	}
}

// TestSenderEnricherRefreshesAfterTTL 超过 TTL 后才重新查询。
func TestSenderEnricherRefreshesAfterTTL(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	src := &fakeUserSource{users: map[int64]*client.UserInfo{
		2001: {UserID: 2001, Username: "old-name"},
	}}
	e := newTestEnricher(src, &now)

	e.Enrich(context.Background(), &types.MessageEvent{SenderID: "2001"})

	now = now.Add(senderInfoTTL + time.Second)
	src.users[2001].Username = "new-name"
	event := &types.MessageEvent{SenderID: "2001"}
	e.Enrich(context.Background(), event)

	if event.SenderName != "new-name" {
		t.Fatalf("name after ttl = %q, want new-name", event.SenderName)
	}
	if calls := src.callCount(); calls != 2 {
		t.Fatalf("user service calls = %d, want 2", calls)
	}
}

// TestSenderEnricherSkipsSystemIdentities 系统身份与非数字 ID 不查用户服务。
func TestSenderEnricherSkipsSystemIdentities(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	src := &fakeUserSource{}
	e := newTestEnricher(src, &now)

	for _, senderID := range []string{"", "system", "bot_helper", "not-a-number"} {
		e.Enrich(context.Background(), &types.MessageEvent{SenderID: senderID})
	}
	if calls := src.callCount(); calls != 0 {
		t.Fatalf("user service calls = %d, want 0", calls)
	}
}

// TestSenderEnricherNegativeCacheOnFailure 下游失败时短暂负缓存，避免每条消息都重试。
func TestSenderEnricherNegativeCacheOnFailure(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	src := &fakeUserSource{err: errors.New("user service down")}
	e := newTestEnricher(src, &now)

	e.Enrich(context.Background(), &types.MessageEvent{SenderID: "3001"})
	e.Enrich(context.Background(), &types.MessageEvent{SenderID: "3001"})
	if calls := src.callCount(); calls != 1 {
		t.Fatalf("calls within negative ttl = %d, want 1", calls)
	}

	now = now.Add(senderInfoNegativeTTL + time.Second)
	e.Enrich(context.Background(), &types.MessageEvent{SenderID: "3001"})
	if calls := src.callCount(); calls != 2 {
		t.Fatalf("calls after negative ttl = %d, want 2", calls)
	}
}

// TestSenderEnricherKeepsUpstreamFields 上游已给出的字段不被覆盖，只补缺失的。
func TestSenderEnricherKeepsUpstreamFields(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	src := &fakeUserSource{users: map[int64]*client.UserInfo{
		4001: {UserID: 4001, Username: "server-name", Avatar: "server.png"},
	}}
	e := newTestEnricher(src, &now)

	event := &types.MessageEvent{SenderID: "4001", SenderName: "client-name"}
	e.Enrich(context.Background(), event)

	if event.SenderName != "client-name" {
		t.Fatalf("upstream name overwritten: %q", event.SenderName)
	}
	if event.SenderAvatar != "server.png" {
		t.Fatalf("avatar = %q, want server.png", event.SenderAvatar)
	}
	if calls := src.callCount(); calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

// TestSenderEnricherBoundedCache 缓存条目数受上限约束，不会无限增长。
func TestSenderEnricherBoundedCache(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	src := &fakeUserSource{users: map[int64]*client.UserInfo{}}
	e := newTestEnricher(src, &now)
	e.maxEntries = 2

	for _, id := range []string{"5001", "5002", "5003"} {
		e.Enrich(context.Background(), &types.MessageEvent{SenderID: id})
	}

	e.mu.Lock()
	size := len(e.cache)
	e.mu.Unlock()
	if size > e.maxEntries {
		t.Fatalf("cache size = %d, want <= %d", size, e.maxEntries)
	}
}
