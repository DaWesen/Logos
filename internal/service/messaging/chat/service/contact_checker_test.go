package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"Logos/pkg/client"
)

type fakeFriendshipClient struct {
	status *client.FriendshipStatus
	err    error
	calls  int
}

func (f *fakeFriendshipClient) IsFriend(_ context.Context, _, _ string) (*client.FriendshipStatus, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.status, nil
}

func newTestContactChecker(c friendshipStatusClient, ttl time.Duration) *ContactCheckerAdapter {
	return &ContactCheckerAdapter{
		client: c,
		cache:  make(map[string]*friendshipCacheEntry),
		ttl:    ttl,
	}
}

func TestContactCheckerCachesWithinTTL(t *testing.T) {
	upstream := &fakeFriendshipClient{status: &client.FriendshipStatus{IsFriend: true}}
	checker := newTestContactChecker(upstream, time.Minute)

	for i := 0; i < 3; i++ {
		result, err := checker.CheckFriendship(context.Background(), "u1", "u2")
		if err != nil {
			t.Fatalf("第 %d 次查询失败: %v", i+1, err)
		}
		if !result.IsFriend {
			t.Fatalf("第 %d 次查询结果错误: %+v", i+1, result)
		}
	}
	if upstream.calls != 1 {
		t.Fatalf("TTL 内应只回源 1 次，实际 %d 次", upstream.calls)
	}
}

// 过期缓存不能在上游故障时继续生效：用户刚拉黑、关系服务又抖动，
// 旧值会让「未拉黑」一直成立，拦截窗口被无限延长。
func TestContactCheckerDoesNotServeExpiredCacheOnError(t *testing.T) {
	upstreamErr := errors.New("contact service unavailable")
	upstream := &fakeFriendshipClient{status: &client.FriendshipStatus{IsBlocked: true}}
	checker := newTestContactChecker(upstream, time.Minute)

	if _, err := checker.CheckFriendship(context.Background(), "u1", "u2"); err != nil {
		t.Fatalf("首次查询失败: %v", err)
	}

	checker.mu.Lock()
	checker.cache[checker.cacheKey("u1", "u2")].cachedAt = time.Now().Add(-2 * time.Minute)
	checker.mu.Unlock()
	upstream.err = upstreamErr

	result, err := checker.CheckFriendship(context.Background(), "u1", "u2")
	if !errors.Is(err, upstreamErr) {
		t.Fatalf("缓存过期且上游失败应返回原错误，实际 result=%+v err=%v", result, err)
	}
	if result != nil {
		t.Fatalf("失败时不得返回过期缓存，实际 %+v", result)
	}
}

func TestContactCheckerRefreshesAfterTTL(t *testing.T) {
	upstream := &fakeFriendshipClient{status: &client.FriendshipStatus{}}
	checker := newTestContactChecker(upstream, time.Minute)

	if _, err := checker.CheckFriendship(context.Background(), "u1", "u2"); err != nil {
		t.Fatalf("首次查询失败: %v", err)
	}

	checker.mu.Lock()
	checker.cache[checker.cacheKey("u1", "u2")].cachedAt = time.Now().Add(-2 * time.Minute)
	checker.mu.Unlock()
	upstream.status = &client.FriendshipStatus{IsBlocked: true}

	result, err := checker.CheckFriendship(context.Background(), "u1", "u2")
	if err != nil {
		t.Fatalf("过期后重查失败: %v", err)
	}
	if !result.IsBlocked || result.Cached {
		t.Fatalf("过期后必须回源拿到最新拉黑状态，实际 %+v", result)
	}
	if upstream.calls != 2 {
		t.Fatalf("过期后应再次回源，实际 %d 次", upstream.calls)
	}
}
