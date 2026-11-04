package jwt

import (
	"testing"
	"time"

	gojwt "github.com/golang-jwt/jwt/v5"
)

func testManager() *JWTManager {
	return &JWTManager{secretKey: "unit-test-secret", tokenExpireHours: 1}
}

func TestParseToken_RoundTrip(t *testing.T) {
	m := testManager()
	token, err := m.GenerateToken("user-1", "user")
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	claims, err := m.ParseToken(token)
	if err != nil {
		t.Fatalf("ParseToken: %v", err)
	}
	if claims.UserID != "user-1" || claims.Role != "user" {
		t.Fatalf("claims = %+v", claims)
	}
}

func TestParseToken_RejectsWrongSecret(t *testing.T) {
	token, err := testManager().GenerateToken("user-1", "user")
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	other := &JWTManager{secretKey: "another-secret", tokenExpireHours: 1}
	if _, err := other.ParseToken(token); err == nil {
		t.Fatal("不同密钥签发的令牌必须被拒绝")
	}
}

func TestParseToken_RejectsExpired(t *testing.T) {
	m := testManager()

	// 同一个密钥，但把过期时间改到过去，模拟过期令牌。
	expired := &JWTManager{secretKey: m.secretKey, tokenExpireHours: -1}
	expiredToken, err := expired.GenerateToken("user-1", "user")
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	if _, err := m.ParseToken(expiredToken); err != ErrExpiredToken {
		t.Fatalf("过期令牌应返回 ErrExpiredToken，实际 %v", err)
	}
}

func TestParseToken_RejectsGarbage(t *testing.T) {
	m := testManager()
	for _, token := range []string{"", "abc", "a.b.c"} {
		if _, err := m.ParseToken(token); err != ErrInvalidToken {
			t.Errorf("ParseToken(%q) err = %v，期望 ErrInvalidToken", token, err)
		}
	}
}

func TestClaimsRemainingTTL(t *testing.T) {
	claims := &Claims{}
	if claims.RemainingTTL() != 0 {
		t.Fatal("缺少 exp 时应返回 0")
	}

	claims.ExpiresAt = gojwt.NewNumericDate(time.Now().Add(time.Hour))
	if ttl := claims.RemainingTTL(); ttl <= 0 || ttl > time.Hour {
		t.Fatalf("ttl = %v", ttl)
	}
}
