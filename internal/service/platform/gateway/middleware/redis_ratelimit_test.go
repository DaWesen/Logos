package middleware

import (
	"testing"
	"time"
)

// TestUserRateLimit_Tiers 限流分档必须彼此不同且越敏感越低：
// 若所有接口共用一个巨大阈值，限流实际不生效。
func TestUserRateLimit_Tiers(t *testing.T) {
	tests := []struct {
		path   string
		method string
		want   int
	}{
		{"/api/v1/auth/login", "POST", 5},
		{"/api/v1/auth/register", "POST", 5},
		{"/api/v1/question/ask", "POST", 30},
		{"/api/v1/extraction/tasks", "POST", 60},
		{"/api/v1/collection/tasks/abc", "POST", 30},
		{"/api/v1/collection/tasks/abc", "PUT", 30},
		{"/api/v1/message/send", "POST", 50},
		{"/api/v1/recommend/feed", "GET", 120},
		{"/api/v1/user/profile", "GET", 600},
	}

	seen := map[int]struct{}{}
	for _, tt := range tests {
		rate, window := userRateLimit(tt.path, tt.method)
		if rate != tt.want {
			t.Errorf("%s %s 阈值 = %d, want %d", tt.method, tt.path, rate, tt.want)
		}
		if window != time.Minute {
			t.Errorf("%s %s 窗口 = %v, want 1m", tt.method, tt.path, window)
		}
		seen[rate] = struct{}{}
	}
	// 分档应当有明显差异（登录 / LLM 问答 / 发消息 / 默认读接口至少 4 档）
	if len(seen) < 4 {
		t.Errorf("限流分档过少（仅 %d 档），各层阈值可能仍然同值", len(seen))
	}

	// 集合任务只有写操作受限，读列表不受低阈值约束
	if rate, _ := userRateLimit("/api/v1/collection/tasks/abc", "GET"); rate != 600 {
		t.Errorf("集合任务 GET 不应套用写操作阈值，实际 %d", rate)
	}
}
