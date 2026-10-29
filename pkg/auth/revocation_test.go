package auth

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"Logos/pkg/cache"
	pkgJwt "Logos/pkg/jwt"
	"Logos/pkg/logger"

	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/zap"
)

// fakeCache 只实现撤销逻辑用到的四个方法，其余接口方法由嵌入的 nil 接口兜底。
type fakeCache struct {
	cache.Cache
	mu     sync.Mutex
	values map[string]string
	err    error
}

func newFakeCache() *fakeCache {
	return &fakeCache{values: map[string]string{}}
}

func (f *fakeCache) Set(_ context.Context, key string, value interface{}, _ time.Duration) error {
	if f.err != nil {
		return f.err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	switch v := value.(type) {
	case string:
		f.values[key] = v
	default:
		f.values[key] = strconv.FormatInt(int64(0), 10)
	}
	return nil
}

func (f *fakeCache) Get(_ context.Context, key string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if v, ok := f.values[key]; ok {
		return v, nil
	}
	return "", errors.New("redis: nil")
}

func (f *fakeCache) Exists(_ context.Context, key string) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.values[key]
	return ok, nil
}

func claimsWithJTI(userID, jti string, issuedAt time.Time) *pkgJwt.Claims {
	c := &pkgJwt.Claims{UserID: userID}
	c.RegisteredClaims.ID = jti
	c.RegisteredClaims.IssuedAt = jwt.NewNumericDate(issuedAt)
	c.RegisteredClaims.ExpiresAt = jwt.NewNumericDate(issuedAt.Add(2 * time.Hour))
	return c
}

func TestRevoker_TokenBlacklist(t *testing.T) {
	ctx := context.Background()
	revoker := NewRevoker(newFakeCache())
	claims := claimsWithJTI("u1", "jti-1", time.Now())

	if revoker.IsRevoked(ctx, claims) {
		t.Fatal("未撤销的令牌不应被判定为失效")
	}
	if err := revoker.RevokeToken(ctx, "jti-1", time.Minute); err != nil {
		t.Fatalf("RevokeToken 失败: %v", err)
	}
	if !revoker.IsRevoked(ctx, claims) {
		t.Fatal("拉黑后的令牌必须判定为失效")
	}
	// 同一用户的另一个令牌不受影响（单设备登出的语义）
	other := claimsWithJTI("u1", "jti-2", time.Now())
	if revoker.IsRevoked(ctx, other) {
		t.Fatal("只应作废被拉黑的那个 jti")
	}
}

func TestRevoker_RevokeAllWatermark(t *testing.T) {
	ctx := context.Background()
	revoker := NewRevoker(newFakeCache())

	oldToken := claimsWithJTI("u2", "jti-old", time.Now().Add(-10*time.Second))
	newToken := claimsWithJTI("u2", "jti-new", time.Now())

	if err := revoker.RevokeAll(ctx, "u2", time.Hour); err != nil {
		t.Fatalf("RevokeAll 失败: %v", err)
	}
	if !revoker.IsRevoked(ctx, oldToken) {
		t.Fatal("水位线之前签发的令牌必须失效")
	}
	if revoker.IsRevoked(ctx, newToken) {
		t.Fatal("撤销后新签发的令牌不应被误杀")
	}
	// 其他用户不受影响
	if revoker.IsRevoked(ctx, claimsWithJTI("u3", "jti-other", time.Now().Add(-time.Hour))) {
		t.Fatal("撤销水位线不能波及其他用户")
	}
}

func TestRevoker_CacheFailureFailOpen(t *testing.T) {
	// 失败分支会写告警日志，先注入空 logger（避免测试依赖 ./config 与日志初始化）
	logger.SetLogger(zap.NewNop())

	ctx := context.Background()
	fake := newFakeCache()
	fake.err = errors.New("redis down")
	revoker := NewRevoker(fake)

	claims := claimsWithJTI("u4", "jti-x", time.Now().Add(-time.Hour))
	// 缓存故障时放行，避免一次抖动导致全站 401；风险由告警兜底
	if revoker.IsRevoked(ctx, claims) {
		t.Fatal("缓存故障时应 fail-open")
	}
}

func TestRevoker_NilSafe(t *testing.T) {
	var revoker *Revoker
	if revoker.IsRevoked(context.Background(), claimsWithJTI("u5", "j", time.Now())) {
		t.Fatal("nil Revoker 不应 panic 或拒绝请求")
	}
	if err := revoker.RevokeToken(context.Background(), "j", time.Minute); err != nil {
		t.Fatalf("nil Revoker 的 RevokeToken 应直接返回 nil: %v", err)
	}
}

func TestClaims_RemainingTTL(t *testing.T) {
	claims := claimsWithJTI("u6", "j", time.Now())
	ttl := claims.RemainingTTL()
	if ttl <= 0 || ttl > 2*time.Hour+time.Minute {
		t.Fatalf("剩余有效期异常: %v", ttl)
	}
	expired := claimsWithJTI("u6", "j2", time.Now().Add(-3*time.Hour))
	if got := expired.RemainingTTL(); got != 0 {
		t.Fatalf("已过期令牌的 TTL 应为 0，实际 %v", got)
	}
}
