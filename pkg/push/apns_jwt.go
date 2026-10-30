package push

import (
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// APNs 的提供方鉴权令牌（provider token）参数。
//
// APNs 的 HTTP/2 接口要求每个请求带 `authorization: bearer <JWT>`，
// JWT 用 .p8 私钥做 ES256 签名，claims 为 `iss = TeamID`、`iat = 签发时间`。
//
// 令牌有效期 1 小时，Apple 建议 20~60 分钟刷新；这里取 50 分钟，
// 既避开「正好 1 小时」的边界（时钟偏差会导致 403 ExpiredProviderToken），
// 又把签名次数压到每小时一次。
//
// 之前的实现只设置了 `apns-topic` / `apns-push-type`，**从没设置授权头**，
// 真实环境所有请求都会被 Apple 以 403 MissingProviderToken 拒绝；
// 单测与本地 mock 不校验鉴权头，所以问题一直没暴露。
const (
	apnsTokenRefreshAfter = 50 * time.Minute
	apnsJWTAlgorithmKey   = "kid"
)

// apnsTokenSource 生成并缓存 APNs provider token。
//
// 缓存是必要的：每条推送都重新签一次 ECDSA，会让 CPU 随消息量线性上升；
// 而 Apple 的令牌本身就可以在 1 小时内复用。
// now 注入是为了单测能精确推进时间、断言「复用 / 到点刷新」。
type apnsTokenSource struct {
	keyID  string
	teamID string
	key    *ecdsa.PrivateKey
	now    func() time.Time

	mu       sync.Mutex
	token    string
	issuedAt time.Time
}

// NewAPNSTokenSource 从配置文件加载 .p8 私钥并构造令牌源。
// 只有在 KeyID / TeamID / KeyPath 都配置时才应调用；缺项返回错误，
// 让调用方决定「不带鉴权头继续」（本地 mock）还是直接失败。
func NewAPNSTokenSource(cfg APNSConfig) (*apnsTokenSource, error) {
	if cfg.KeyID == "" || cfg.TeamID == "" || cfg.KeyPath == "" {
		return nil, errors.New("push: APNs 鉴权需要 key_id / team_id / key_path 全部配置")
	}
	data, err := os.ReadFile(cfg.KeyPath)
	if err != nil {
		return nil, fmt.Errorf("push: 读取 APNs 私钥失败: %w", err)
	}
	key, err := parseAPNSPrivateKey(data)
	if err != nil {
		return nil, err
	}
	return newAPNSTokenSource(cfg, key, time.Now), nil
}

// newAPNSTokenSource 是可直接注入私钥与时钟的构造函数（供单测使用）。
func newAPNSTokenSource(cfg APNSConfig, key *ecdsa.PrivateKey, now func() time.Time) *apnsTokenSource {
	return &apnsTokenSource{
		keyID:  cfg.KeyID,
		teamID: cfg.TeamID,
		key:    key,
		now:    now,
	}
}

// parseAPNSPrivateKey 解析 .p8 文件内容。
// Apple 下发的是 PKCS#8 编码的 ECDSA 私钥；这里同时兼容 SEC1 编码，
// 以便测试与历史密钥文件可用。
func parseAPNSPrivateKey(data []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("push: APNs 私钥不是 PEM 格式")
	}
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		ecKey, ok := k.(*ecdsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("push: APNs 私钥类型为 %T，需要 ECDSA（ES256）", k)
		}
		return ecKey, nil
	}
	if k, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	return nil, errors.New("push: 无法解析 APNs 私钥")
}

// Token 返回可用的 provider token：未过期直接复用，过期则重新签一份。
func (s *apnsTokenSource) Token() (string, error) {
	if s == nil || s.key == nil {
		return "", errors.New("push: APNs 令牌源未初始化")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	if s.token != "" && now.Sub(s.issuedAt) < apnsTokenRefreshAfter {
		return s.token, nil
	}

	claims := jwt.MapClaims{
		"iss": s.teamID,
		"iat": now.Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
	// kid 必须在 header 里，Apple 靠它定位团队里的那把私钥
	token.Header[apnsJWTAlgorithmKey] = s.keyID

	signed, err := token.SignedString(s.key)
	if err != nil {
		return "", fmt.Errorf("push: 签发 APNs provider token 失败: %w", err)
	}

	s.token = signed
	s.issuedAt = now
	return signed, nil
}
