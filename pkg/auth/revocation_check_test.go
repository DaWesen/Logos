package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"Logos/pkg/logger"

	"go.uber.org/zap"
)

// TestCheck_States 三态结果必须可区分：
//   - 未撤销 -> Clear
//   - 命中单令牌黑名单 / 早于用户水位线 -> Revoked
//   - 缓存故障 / 水位线脏数据 -> Unknown + error
func TestCheck_States(t *testing.T) {
	logger.SetLogger(zap.NewNop())
	ctx := context.Background()

	t.Run("clear", func(t *testing.T) {
		revoker := NewRevoker(newFakeCache())
		state, err := revoker.Check(ctx, claimsWithJTI("u1", "jti-1", time.Now()))
		if err != nil || state != RevocationClear {
			t.Fatalf("Check = (%v, %v), want (clear, nil)", state, err)
		}
	})

	t.Run("token revoked", func(t *testing.T) {
		fake := newFakeCache()
		revoker := NewRevoker(fake)
		if err := revoker.RevokeToken(ctx, "jti-2", time.Minute); err != nil {
			t.Fatalf("RevokeToken: %v", err)
		}
		state, err := revoker.Check(ctx, claimsWithJTI("u2", "jti-2", time.Now()))
		if err != nil || state != RevocationRevoked {
			t.Fatalf("Check = (%v, %v), want (revoked, nil)", state, err)
		}
	})

	t.Run("user watermark revoked", func(t *testing.T) {
		revoker := NewRevoker(newFakeCache())
		if err := revoker.RevokeAll(ctx, "u3", time.Hour); err != nil {
			t.Fatalf("RevokeAll: %v", err)
		}
		old := claimsWithJTI("u3", "jti-old", time.Now().Add(-time.Minute))
		state, err := revoker.Check(ctx, old)
		if err != nil || state != RevocationRevoked {
			t.Fatalf("Check = (%v, %v), want (revoked, nil)", state, err)
		}
		// 水位线之后签发的令牌仍然有效（< 而非 <=）
		fresh := claimsWithJTI("u3", "jti-new", time.Now().Add(time.Second))
		state, err = revoker.Check(ctx, fresh)
		if err != nil || state != RevocationClear {
			t.Fatalf("新令牌 Check = (%v, %v), want (clear, nil)", state, err)
		}
	})

	t.Run("cache unavailable", func(t *testing.T) {
		fake := newFakeCache()
		fake.err = errors.New("redis down")
		revoker := NewRevoker(fake)
		state, err := revoker.Check(ctx, claimsWithJTI("u4", "jti-4", time.Now()))
		if state != RevocationUnknown {
			t.Fatalf("缓存故障应返回 unknown，实际 %v", state)
		}
		if err == nil {
			t.Fatal("缓存故障必须返回 error，供调用方打点告警")
		}
	})

	t.Run("corrupt watermark", func(t *testing.T) {
		fake := newFakeCache()
		fake.values[revokedUserPrefix+"u5"] = "not-a-number"
		revoker := NewRevoker(fake)
		state, err := revoker.Check(ctx, claimsWithJTI("u5", "jti-5", time.Now()))
		if state != RevocationUnknown || err == nil {
			t.Fatalf("脏水位线应返回 (unknown, error)，实际 (%v, %v)", state, err)
		}
	})

	t.Run("nil safe", func(t *testing.T) {
		var revoker *Revoker
		state, err := revoker.Check(ctx, claimsWithJTI("u6", "j", time.Now()))
		if state != RevocationClear || err != nil {
			t.Fatalf("nil Revoker Check = (%v, %v), want (clear, nil)", state, err)
		}
		state, err = revoker.Check(ctx, nil)
		if state != RevocationClear || err != nil {
			t.Fatalf("nil claims Check = (%v, %v), want (clear, nil)", state, err)
		}
	})
}

// TestRevocationStateString 指标标签必须稳定，否则同一状态会以多种标签进入监控。
func TestRevocationStateString(t *testing.T) {
	cases := map[RevocationState]string{
		RevocationClear:   "clear",
		RevocationRevoked: "revoked",
		RevocationUnknown: "unknown",
	}
	for state, want := range cases {
		if got := state.String(); got != want {
			t.Fatalf("RevocationState(%d).String() = %q, want %q", int(state), got, want)
		}
	}
}
