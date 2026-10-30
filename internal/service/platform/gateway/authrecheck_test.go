package gateway

import (
	"testing"
	"time"

	"Logos/pkg/jwt"

	jwtlib "github.com/golang-jwt/jwt/v5"
)

func activeClaims(exp time.Time) *jwt.Claims {
	return &jwt.Claims{
		UserID: "u1",
		RegisteredClaims: jwtlib.RegisteredClaims{
			ExpiresAt: jwtlib.NewNumericDate(exp),
		},
	}
}

// TestAuthRecheckDueExactBoundary 验证「恰好到达间隔」即视为到期：
// 用 >= 而不是严格大于，避免在边界上永远等不到下一次复核。
func TestAuthRecheckDueExactBoundary(t *testing.T) {
	last := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	interval := 5 * time.Minute

	if AuthRecheckDue(last, last.Add(4*time.Minute+59*time.Second), interval) {
		t.Fatal("未到间隔不应复核")
	}
	if !AuthRecheckDue(last, last.Add(interval), interval) {
		t.Fatal("恰好到达间隔应复核")
	}
	if !AuthRecheckDue(last, last.Add(interval+time.Second), interval) {
		t.Fatal("超过间隔应复核")
	}
}

// TestAuthRecheckDueZeroValue 零值 last 表示「尚未复核」，必须立即复核；
// 若误判为「刚复核过」，新连接在首个周期结束前都不会做撤销检查。
func TestAuthRecheckDueZeroValue(t *testing.T) {
	if !AuthRecheckDue(time.Time{}, time.Now(), time.Minute) {
		t.Fatal("零值 last 应视为到期")
	}
}

// TestAuthRecheckDueDisabledInterval 非正间隔表示关闭复核（便于配置化降级）。
func TestAuthRecheckDueDisabledInterval(t *testing.T) {
	last := time.Now().Add(-time.Hour)
	if AuthRecheckDue(last, time.Now(), 0) {
		t.Fatal("interval<=0 时不应复核")
	}
}

// TestClassifyAuthFailurePriority 验证失败原因的判定顺序：
// 无效声明 > 已撤销 > 已过期 > 通过。
func TestClassifyAuthFailurePriority(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name    string
		claims  *jwt.Claims
		revoked bool
		want    AuthFailureReason
	}{
		{"nil 声明", nil, false, AuthFailureInvalid},
		{"空 user_id", &jwt.Claims{}, false, AuthFailureInvalid},
		{"已撤销（即使也过期）", activeClaims(now.Add(-time.Minute)), true, AuthFailureRevoked},
		{"有效令牌", activeClaims(now.Add(time.Hour)), false, AuthFailureNone},
		{"已过期", activeClaims(now.Add(-time.Minute)), false, AuthFailureExpired},
		{"缺少 exp", &jwt.Claims{UserID: "u1"}, false, AuthFailureExpired},
		{"恰好在 exp 时刻", activeClaims(now), false, AuthFailureExpired},
	}

	for _, tc := range cases {
		if got := ClassifyAuthFailure(tc.claims, tc.revoked, now); got != tc.want {
			t.Fatalf("%s: 期望 %q，实际 %q", tc.name, tc.want, got)
		}
	}
}
