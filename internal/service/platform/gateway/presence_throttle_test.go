package gateway

import (
	"testing"
	"time"
)

// TestShouldTouchPresenceInterval 验证节流的边界：间隔之内跳过，到达间隔立即续期。
func TestShouldTouchPresenceInterval(t *testing.T) {
	last := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	interval := 15 * time.Second

	if ShouldTouchPresence(last, last.Add(14*time.Second), interval) {
		t.Fatal("间隔内不应续期")
	}
	if !ShouldTouchPresence(last, last.Add(interval), interval) {
		t.Fatal("恰好到达间隔应续期")
	}
	if !ShouldTouchPresence(last, last.Add(interval+time.Second), interval) {
		t.Fatal("超过间隔应续期")
	}
}

// TestShouldTouchPresenceZeroValue 首次续期必须放行。
func TestShouldTouchPresenceZeroValue(t *testing.T) {
	if !ShouldTouchPresence(time.Time{}, time.Now(), time.Minute) {
		t.Fatal("零值 last 应立即续期")
	}
}

// TestShouldTouchPresenceDisabled 非正间隔表示关闭节流（每次都续期，宁可多写）。
func TestShouldTouchPresenceDisabled(t *testing.T) {
	last := time.Now()
	if !ShouldTouchPresence(last, last, 0) {
		t.Fatal("interval<=0 时应每次都续期")
	}
}

// TestPresenceTouchIntervalInvariant 节流间隔必须显著小于 presence 过期窗口，
// 否则「跳过续期」本身就会把健康会话判成离线。
func TestPresenceTouchIntervalInvariant(t *testing.T) {
	if PresenceTouchInterval <= 0 {
		t.Fatalf("续期间隔必须为正，实际 %s", PresenceTouchInterval)
	}
	if PresenceTouchInterval >= PresenceStaleAfter/2 {
		t.Fatalf("续期间隔(%s)必须小于过期窗口的一半(%s)，保证跳过若干次也不会过期",
			PresenceTouchInterval, PresenceStaleAfter/2)
	}
	// 最坏情况：一次续期之后恰好跳过整个节流窗口，再叠加一次读超时（连接已半死，
	// 但尚未被判离线），总时长仍必须小于过期窗口。
	if worst := PresenceTouchInterval + TCPReadTimeout; worst >= PresenceStaleAfter {
		t.Fatalf("续期间隔+读超时(%s)必须小于过期窗口(%s)", worst, PresenceStaleAfter)
	}
}
