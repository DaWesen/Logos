package push

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func generateRSAKey() (*rsa.PrivateKey, error) {
	return rsa.GenerateKey(rand.Reader, 2048)
}

func generateP256Key(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("生成 P-256 私钥失败: %v", err)
	}
	return key
}

func encodePKCS8PEM(t *testing.T, key *ecdsa.PrivateKey) []byte {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("PKCS#8 编码失败: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

func TestAPNSTokenSource_SignsES256(t *testing.T) {
	key := generateP256Key(t)
	now := time.Unix(1700000000, 0)
	src := newAPNSTokenSource(APNSConfig{KeyID: "KEY123", TeamID: "TEAM456"}, key, func() time.Time { return now })

	token, err := src.Token()
	if err != nil {
		t.Fatalf("签发 provider token 失败: %v", err)
	}

	parsed, err := jwt.Parse(token, func(tk *jwt.Token) (interface{}, error) {
		if tk.Method.Alg() != jwt.SigningMethodES256.Alg() {
			t.Fatalf("签名算法应为 ES256，实际 %s", tk.Method.Alg())
		}
		return &key.PublicKey, nil
	})
	if err != nil {
		t.Fatalf("校验 provider token 失败: %v", err)
	}

	kid, ok := parsed.Header["kid"].(string)
	if !ok || kid != "KEY123" {
		t.Fatalf("header.kid 应为 KEY123，实际 %v", parsed.Header["kid"])
	}

	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		t.Fatalf("claims 类型异常: %T", parsed.Claims)
	}
	if iss, _ := claims["iss"].(string); iss != "TEAM456" {
		t.Fatalf("iss 应为 TEAM456，实际 %v", claims["iss"])
	}
	iat, ok := claims["iat"].(float64)
	if !ok || int64(iat) != now.Unix() {
		t.Fatalf("iat 应为 %d，实际 %v", now.Unix(), claims["iat"])
	}
}

func TestAPNSTokenSource_ReusesThenRefreshes(t *testing.T) {
	key := generateP256Key(t)
	base := time.Unix(1700000000, 0)
	current := base
	src := newAPNSTokenSource(APNSConfig{KeyID: "K", TeamID: "T"}, key, func() time.Time { return current })

	first, err := src.Token()
	if err != nil {
		t.Fatalf("首次签发失败: %v", err)
	}

	// 未到刷新窗口：必须复用同一个字符串（避免每条推送都做一次 ECDSA 签名）
	current = base.Add(49 * time.Minute)
	second, err := src.Token()
	if err != nil {
		t.Fatalf("复用阶段签发失败: %v", err)
	}
	if second != first {
		t.Fatal("50 分钟内应复用同一个 provider token")
	}

	// 超过刷新窗口：必须重新签发，iat 随之前进
	current = base.Add(51 * time.Minute)
	third, err := src.Token()
	if err != nil {
		t.Fatalf("刷新阶段签发失败: %v", err)
	}
	if third == first {
		t.Fatal("超过刷新窗口后应重新签发 token")
	}

	parsed, err := jwt.Parse(third, func(*jwt.Token) (interface{}, error) { return &key.PublicKey, nil })
	if err != nil {
		t.Fatalf("刷新后的 token 校验失败: %v", err)
	}
	claims := parsed.Claims.(jwt.MapClaims)
	if iat, _ := claims["iat"].(float64); int64(iat) != current.Unix() {
		t.Fatalf("刷新后 iat 应为 %d，实际 %v", current.Unix(), claims["iat"])
	}
}

func TestParseAPNSPrivateKey_PKCS8AndSEC1(t *testing.T) {
	key := generateP256Key(t)

	if _, err := parseAPNSPrivateKey(encodePKCS8PEM(t, key)); err != nil {
		t.Fatalf("PKCS#8 私钥应可解析: %v", err)
	}

	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("SEC1 编码失败: %v", err)
	}
	sec1 := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
	if _, err := parseAPNSPrivateKey(sec1); err != nil {
		t.Fatalf("SEC1 私钥应可解析: %v", err)
	}

	if _, err := parseAPNSPrivateKey([]byte("not pem")); err == nil {
		t.Fatal("非 PEM 内容应返回错误")
	}
}

func TestParseAPNSPrivateKey_RejectsNonECDSA(t *testing.T) {
	// RSA 私钥必须被拒绝：APNs 只接受 ES256
	rsaKey, err := generateRSAKey()
	if err != nil {
		t.Skipf("生成 RSA 私钥失败: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(rsaKey)
	if err != nil {
		t.Fatalf("编码 RSA 私钥失败: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	if _, err := parseAPNSPrivateKey(pemBytes); err == nil {
		t.Fatal("非 ECDSA 私钥应返回错误")
	}
}

func TestNewAPNSTokenSource_RequiresConfig(t *testing.T) {
	if _, err := NewAPNSTokenSource(APNSConfig{}); err == nil {
		t.Fatal("完全空配置应返回错误")
	}
	if _, err := NewAPNSTokenSource(APNSConfig{KeyID: "K", TeamID: "T"}); err == nil {
		t.Fatal("缺 KeyPath 应返回错误")
	}
}

func TestNewAPNSTokenSource_LoadsP8File(t *testing.T) {
	key := generateP256Key(t)
	path := filepath.Join(t.TempDir(), "AuthKey.p8")
	if err := os.WriteFile(path, encodePKCS8PEM(t, key), 0600); err != nil {
		t.Fatalf("写入 .p8 文件失败: %v", err)
	}

	src, err := NewAPNSTokenSource(APNSConfig{KeyID: "K", TeamID: "T", KeyPath: path})
	if err != nil {
		t.Fatalf("从 .p8 加载令牌源失败: %v", err)
	}
	if _, err := src.Token(); err != nil {
		t.Fatalf("加载后签发失败: %v", err)
	}

	if _, err := NewAPNSTokenSource(APNSConfig{KeyID: "K", TeamID: "T", KeyPath: path + ".missing"}); err == nil {
		t.Fatal("私钥文件不存在应返回错误")
	}
}

func TestAPNSTokenSource_NilSafe(t *testing.T) {
	var src *apnsTokenSource
	if _, err := src.Token(); err == nil {
		t.Fatal("nil 令牌源应返回错误而不是 panic")
	}
}
