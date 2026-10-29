package jwt

import (
	"errors"
	"time"

	"Logos/config"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

var (
	ErrInvalidToken        = errors.New("invalid token")
	ErrExpiredToken        = errors.New("token expired")
	ErrInvalidRefreshToken = errors.New("invalid refresh token")
	ErrExpiredRefreshToken = errors.New("refresh token expired")
)

type Claims struct {
	UserID string `json:"user_id"`
	Role   string `json:"role"`
	jwt.RegisteredClaims
}

type RefreshClaims struct {
	UserID string `json:"user_id"`
	jwt.RegisteredClaims
}

type JWTManager struct {
	secretKey         string
	tokenExpireHours  int
	refreshExpireDays int
}

func NewJWTManager() *JWTManager {
	cfg := config.GetConfig()
	return &JWTManager{
		secretKey:         cfg.JWT.Secret,
		tokenExpireHours:  cfg.JWT.ExpireHours,
		refreshExpireDays: 14,
	}
}

func (m *JWTManager) GetTokenExpireHours() int {
	return m.tokenExpireHours
}

// RemainingTTL 返回令牌剩余有效期；已过期或缺少 exp 时返回 0。
// 登出拉黑时用它作为 Redis TTL：令牌自然过期后黑名单自动清理，不会无限堆积。
func (c *Claims) RemainingTTL() time.Duration {
	if c == nil || c.ExpiresAt == nil {
		return 0
	}
	ttl := time.Until(c.ExpiresAt.Time)
	if ttl < 0 {
		return 0
	}
	return ttl
}

// GenerateToken 生成访问令牌
func (m *JWTManager) GenerateToken(userID, role string) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID: userID,
		Role:   role,
		RegisteredClaims: jwt.RegisteredClaims{
			// jti：每个令牌唯一标识，登出时按它拉黑单个令牌（不需要为每个 token 建索引）
			ID:        uuid.NewString(),
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Duration(m.tokenExpireHours) * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(m.secretKey))
	if err != nil {
		return "", err
	}

	return tokenString, nil
}

// ParseToken 解析访问令牌
func (m *JWTManager) ParseToken(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		// 固定算法：只接受 HS256，避免 alg 混淆（例如把 header 改成 none / RS256 骗过校验）
		if token.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, ErrInvalidToken
		}
		return []byte(m.secretKey), nil
	})

	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrExpiredToken
		}
		return nil, ErrInvalidToken
	}

	if claims, ok := token.Claims.(*Claims); ok && token.Valid {
		return claims, nil
	}

	return nil, ErrInvalidToken
}

// GenerateRefreshToken 生成刷新令牌
func (m *JWTManager) GenerateRefreshToken(userID string) (string, error) {
	claims := RefreshClaims{
		UserID: userID,
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        uuid.NewString(),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Duration(m.refreshExpireDays) * 24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			NotBefore: jwt.NewNumericDate(time.Now()),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(m.secretKey))
	if err != nil {
		return "", err
	}

	return tokenString, nil
}

// ParseRefreshToken 解析刷新令牌
func (m *JWTManager) ParseRefreshToken(tokenString string) (*RefreshClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &RefreshClaims{}, func(token *jwt.Token) (interface{}, error) {
		return []byte(m.secretKey), nil
	})

	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrExpiredRefreshToken
		}
		return nil, ErrInvalidRefreshToken
	}

	if claims, ok := token.Claims.(*RefreshClaims); ok && token.Valid {
		return claims, nil
	}

	return nil, ErrInvalidRefreshToken
}

// RefreshToken 使用刷新令牌获取新的访问令牌
func (m *JWTManager) RefreshToken(refreshToken, role string) (string, error) {
	claims, err := m.ParseRefreshToken(refreshToken)
	if err != nil {
		return "", err
	}

	return m.GenerateToken(claims.UserID, role)
}
