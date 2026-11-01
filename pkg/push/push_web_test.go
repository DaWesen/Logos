package push

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// generateSubscription 模拟浏览器 PushManager.subscribe 的产物：
// 客户端持有一对 P-256 密钥，把公钥（65 字节未压缩点）与 16 字节 auth 上传给服务端。
func generateSubscription(t *testing.T) (uaPrivate *ecdh.PrivateKey, data map[string]string) {
	t.Helper()
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("生成客户端密钥失败: %v", err)
	}
	auth := make([]byte, 16)
	if _, err := rand.Read(auth); err != nil {
		t.Fatalf("生成 auth 失败: %v", err)
	}
	return key, map[string]string{
		webPushKeyP256DH: base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()),
		webPushKeyAuth:   base64.RawURLEncoding.EncodeToString(auth),
	}
}

func newVAPIDConfig(t *testing.T) WebPushConfig {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("生成 VAPID 密钥失败: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("序列化 VAPID 私钥失败: %v", err)
	}
	return WebPushConfig{
		VAPIDPrivateKey: base64.RawURLEncoding.EncodeToString(der),
		Subject:         "mailto:push@example.com",
	}
}

// decryptWebPushPayload 用客户端私钥解开服务端下发的 aes128gcm 载荷。
// 这是唯一能证明「加密真的符合 RFC 8291」的验证方式——只看状态码证明不了浏览器能解。
func decryptWebPushPayload(t *testing.T, body []byte, uaPrivate *ecdh.PrivateKey, authSecret []byte) []byte {
	t.Helper()
	if len(body) < 21 {
		t.Fatalf("载荷过短: %d 字节", len(body))
	}
	salt := body[:16]
	recordSize := binary.BigEndian.Uint32(body[16:20])
	if recordSize < 18 {
		t.Fatalf("record size 非法: %d", recordSize)
	}
	idLen := int(body[20])
	if len(body) < 21+idLen {
		t.Fatalf("idlen=%d 超出载荷长度", idLen)
	}
	asPublic := body[21 : 21+idLen]
	ciphertext := body[21+idLen:]

	curve := ecdh.P256()
	asKey, err := curve.NewPublicKey(asPublic)
	if err != nil {
		t.Fatalf("解析服务端公钥失败: %v", err)
	}
	shared, err := uaPrivate.ECDH(asKey)
	if err != nil {
		t.Fatalf("计算共享密钥失败: %v", err)
	}
	uaPublic := uaPrivate.PublicKey().Bytes()

	keyInfo := make([]byte, 0, len("WebPush: info\x00")+len(uaPublic)+len(asPublic))
	keyInfo = append(keyInfo, []byte("WebPush: info\x00")...)
	keyInfo = append(keyInfo, uaPublic...)
	keyInfo = append(keyInfo, asPublic...)

	ikm := hkdfSHA256(authSecret, shared, keyInfo, 32)
	cek := hkdfSHA256(salt, ikm, []byte("Content-Encoding: aes128gcm\x00"), 16)
	nonce := hkdfSHA256(salt, ikm, []byte("Content-Encoding: nonce\x00"), 12)

	block, err := aes.NewCipher(cek)
	if err != nil {
		t.Fatalf("构造 AES 失败: %v", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("构造 GCM 失败: %v", err)
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		t.Fatalf("解密失败: %v", err)
	}
	if len(plaintext) == 0 || plaintext[len(plaintext)-1] != webPushPayloadDelimiter {
		t.Fatalf("缺少结束标记 0x02: %v", plaintext)
	}
	return plaintext[:len(plaintext)-1]
}

func TestWebPushProviderSend_EndToEnd(t *testing.T) {
	uaPrivate, subscription := generateSubscription(t)
	authSecret, _ := decodeBase64URL(subscription[webPushKeyAuth])

	var (
		gotBody   []byte
		gotHeader http.Header
		gotPath   string
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotHeader = r.Header.Clone()
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	provider := NewWebPushProvider(newVAPIDConfig(t))
	msg := &PushMessage{
		ID:       "notif_1",
		UserID:   "u1",
		Platform: PlatformWeb,
		Token:    server.URL + "/push/sub-1",
		Title:    "你有新消息",
		Body:     "来自 张三",
		Data:     subscription,
	}

	result, err := provider.Send(context.Background(), msg)
	if err != nil {
		t.Fatalf("发送失败: %v", err)
	}
	if !result.Success {
		t.Fatalf("应发送成功，实际 %+v", result)
	}
	if gotPath != "/push/sub-1" {
		t.Fatalf("端点路径 = %q", gotPath)
	}
	if gotHeader.Get("Content-Encoding") != "aes128gcm" {
		t.Fatalf("Content-Encoding = %q", gotHeader.Get("Content-Encoding"))
	}
	if gotHeader.Get("TTL") == "" {
		t.Fatal("缺少 TTL 头")
	}
	authz := gotHeader.Get("Authorization")
	if !strings.HasPrefix(authz, "vapid t=") || !strings.Contains(authz, ",k=") {
		t.Fatalf("Authorization 格式错误: %q", authz)
	}

	plaintext := decryptWebPushPayload(t, gotBody, uaPrivate, authSecret)
	var payload map[string]interface{}
	if err := json.Unmarshal(plaintext, &payload); err != nil {
		t.Fatalf("解密后的载荷不是 JSON: %v", err)
	}
	if payload["title"] != msg.Title || payload["body"] != msg.Body {
		t.Fatalf("解密内容不匹配: %v", payload)
	}
}

func TestWebPushProviderSend_StatusClassification(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		wantKind  PushFailureKind
		wantEvict bool
	}{
		{"410 订阅已失效", http.StatusGone, PushFailurePermanent, true},
		{"404 订阅不存在", http.StatusNotFound, PushFailurePermanent, true},
		{"429 限流", http.StatusTooManyRequests, PushFailureRetryable, false},
		{"503 服务端故障", http.StatusServiceUnavailable, PushFailureRetryable, false},
		{"400 请求非法", http.StatusBadRequest, PushFailurePermanent, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, subscription := generateSubscription(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte("error"))
			}))
			defer server.Close()

			provider := NewWebPushProvider(newVAPIDConfig(t))
			result, err := provider.Send(context.Background(), &PushMessage{
				UserID:   "u1",
				Platform: PlatformWeb,
				Token:    server.URL + "/push/sub-1",
				Title:    "t",
				Body:     "b",
				Data:     subscription,
			})
			if err == nil {
				t.Fatal("非 2xx 应返回错误")
			}
			if result.Failure.Kind != tc.wantKind || result.Failure.ShouldUnregister() != tc.wantEvict {
				t.Fatalf("分类 = %s / evict=%v，期望 %s / evict=%v",
					result.Failure.Kind, result.Failure.ShouldUnregister(), tc.wantKind, tc.wantEvict)
			}
		})
	}
}

func TestWebPushProviderSend_MissingSubscription(t *testing.T) {
	provider := NewWebPushProvider(newVAPIDConfig(t))
	result, err := provider.Send(context.Background(), &PushMessage{
		UserID:   "u1",
		Platform: PlatformWeb,
		Token:    "https://push.example.com/sub-1",
		Title:    "t",
	})
	if !errors.Is(err, ErrWebPushSubscriptionInvalid) {
		t.Fatalf("应返回 ErrWebPushSubscriptionInvalid，实际 %v", err)
	}
	if result.Failure.Kind != PushFailurePermanent || result.Failure.ShouldUnregister() {
		t.Fatalf("缺少订阅材料应永久失败但不注销，实际 kind=%s evict=%v",
			result.Failure.Kind, result.Failure.ShouldUnregister())
	}
}

func TestWebPushProviderSend_NotConfigured(t *testing.T) {
	_, subscription := generateSubscription(t)
	provider := NewWebPushProvider(WebPushConfig{})
	result, err := provider.Send(context.Background(), &PushMessage{
		UserID:   "u1",
		Platform: PlatformWeb,
		Token:    "https://push.example.com/sub-1",
		Title:    "t",
		Data:     subscription,
	})
	if !errors.Is(err, ErrWebPushNotConfigured) {
		t.Fatalf("应返回 ErrWebPushNotConfigured，实际 %v", err)
	}
	if result.Success || result.Failure.ShouldUnregister() {
		t.Fatal("未配置 VAPID 不得报告成功，也不得注销订阅")
	}
}

// TestSendToUser_InjectsWebPushKeys 守住集成点：密钥必须从 PushToken 流到 provider。
// 历史实现只注册 token 不带订阅密钥，provider 即使实现了加密也拿不到 key。
func TestSendToUser_InjectsWebPushKeys(t *testing.T) {
	captured := &webCaptureProvider{}
	manager := &PushManager{
		providers:          map[Platform]PushProvider{PlatformWeb: captured},
		tokens:             make(map[string][]*PushToken),
		sendConcurrency:    DefaultSendConcurrency,
		perUserConcurrency: DefaultPerUserConcurrency,
	}
	manager.RegisterToken(&PushToken{
		UserID:   "u1",
		DeviceID: "d1",
		Platform: PlatformWeb,
		Token:    "https://push.example.com/sub-1",
		WebPushKeys: &WebPushKeys{
			P256DH: "p256dh-test",
			Auth:   "auth-test",
		},
	})

	results := manager.SendToUser(context.Background(), "u1", "t", "b", map[string]string{"chat_id": "c1"})
	if len(results) != 1 || !results[0].Success {
		t.Fatalf("结果异常: %+v", results)
	}
	if captured.data[webPushKeyP256DH] != "p256dh-test" || captured.data[webPushKeyAuth] != "auth-test" {
		t.Fatalf("订阅密钥未注入: %v", captured.data)
	}
	if captured.data["chat_id"] != "c1" {
		t.Fatalf("业务字段被覆盖: %v", captured.data)
	}
}

type webCaptureProvider struct {
	data map[string]string
}

func (p *webCaptureProvider) Name() string { return "webpush-capture" }

func (p *webCaptureProvider) Send(ctx context.Context, msg *PushMessage) (*PushResult, error) {
	p.data = msg.Data
	return &PushResult{UserID: msg.UserID, Success: true}, nil
}

func TestWebPushProviderName(t *testing.T) {
	if got := NewWebPushProvider(WebPushConfig{}).Name(); got != "webpush" {
		t.Fatalf("provider name = %q, want %q", got, "webpush")
	}
}

// TestParseVAPIDPrivateKey 三种配置写法都要能解析：PEM、base64url 裸 DER、非法输入。
func TestParseVAPIDPrivateKey(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("生成密钥失败: %v", err)
	}
	der, _ := x509.MarshalPKCS8PrivateKey(key)

	if parsed, err := parseVAPIDPrivateKey(base64.RawURLEncoding.EncodeToString(der)); err != nil || parsed.D.Cmp(key.D) != 0 {
		t.Fatalf("base64url DER 解析失败: %v", err)
	}
	pemBytes := pemEncodePrivateKey(t, der)
	if parsed, err := parseVAPIDPrivateKey(string(pemBytes)); err != nil || parsed.D.Cmp(key.D) != 0 {
		t.Fatalf("PEM 解析失败: %v", err)
	}
	if _, err := parseVAPIDPrivateKey("not-a-key"); err == nil {
		t.Fatal("非法输入必须报错")
	}
}

// pemEncodePrivateKey 用标准库包装成 PEM，避免测试自己拼头尾字符串。
func pemEncodePrivateKey(t *testing.T, der []byte) []byte {
	t.Helper()
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}
