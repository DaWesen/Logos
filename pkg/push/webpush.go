package push

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Web Push 协议常量。
const (
	// webPushRecordSize 单条记录的 rs 字段。RFC 8188 要求首记录不小于 18 字节；
	// 通知载荷远小于 4 KiB，用一个固定值即可，不做分片。
	webPushRecordSize = 4096
	// webPushTTL 通知在推送服务（FCM/APNs/自建）侧最多保留的秒数。
	webPushTTL = 3600
	// webPushVAPIDExpiry VAPID JWT 有效期。规范上限 24 小时，
	// 取 12 小时是为了留出时钟漂移余量，同时避免 token 缓存过久。
	webPushVAPIDExpiry = 12 * time.Hour
	// webPushPayloadDelimiter 单条记录结束标记（RFC 8188）。
	webPushPayloadDelimiter = 0x02
	// webPushBodyReadLimit 读取推送服务错误响应的上限，避免恶意端点返回超大 body。
	webPushBodyReadLimit = 512
)

// 订阅密钥在 Data 里的键名，由 SendToUser 从 PushToken.WebPushKeys 注入。
const (
	webPushKeyEndpoint = "webpush_endpoint"
	webPushKeyP256DH   = "webpush_p256dh"
	webPushKeyAuth     = "webpush_auth"
)

var (
	// ErrWebPushNotConfigured VAPID 密钥或 subject 缺失。
	ErrWebPushNotConfigured = errors.New("webpush: VAPID 密钥或 subject 未配置")
	// ErrWebPushSubscriptionInvalid 订阅端缺少 p256dh / auth，或格式非法。
	ErrWebPushSubscriptionInvalid = errors.New("webpush: 订阅密钥缺失或格式非法")
)

// Send 按 RFC 8291（aes128gcm）+ RFC 8030 发送一条 Web Push。
//
// 三步缺一不可：用订阅的 p256dh/auth 加密载荷（浏览器才能解出明文）、
// 用 VAPID 私钥签 JWT 证明「这个推送服务归我们所有」、PUT/POST 到订阅端点。
// 历史实现直接返回成功，实际上一条请求都没发——Web 端离线通知从未送达。
func (p *WebPushProvider) Send(ctx context.Context, msg *PushMessage) (*PushResult, error) {
	if p.config.Subject == "" || p.config.VAPIDPrivateKey == "" {
		return p.failureResult(msg, http.StatusInternalServerError, "", ErrWebPushNotConfigured,
			PushFailure{Kind: PushFailurePermanent, Reason: "webpush not configured"})
	}

	endpoint, uaPublic, authSecret, err := parseWebPushSubscription(msg)
	if err != nil {
		return p.failureResult(msg, http.StatusBadRequest, "", err,
			PushFailure{Kind: PushFailurePermanent, Reason: err.Error()})
	}

	payload, err := json.Marshal(map[string]interface{}{
		"id":    msg.ID,
		"title": msg.Title,
		"body":  msg.Body,
		"data":  msg.Data,
	})
	if err != nil {
		return p.failureResult(msg, 0, err.Error(), err,
			PushFailure{Kind: PushFailurePermanent, Reason: "marshal payload failed"})
	}

	body, err := encryptWebPushPayload(uaPublic, authSecret, payload)
	if err != nil {
		return p.failureResult(msg, 0, err.Error(), err,
			PushFailure{Kind: PushFailurePermanent, Reason: "encrypt payload failed"})
	}

	authHeader, err := p.vapidAuthorization(endpoint)
	if err != nil {
		return p.failureResult(msg, 0, err.Error(), err,
			PushFailure{Kind: PushFailurePermanent, Reason: "vapid sign failed"})
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return p.failureResult(msg, 0, err.Error(), err,
			PushFailure{Kind: PushFailurePermanent, Reason: "build request failed"})
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("TTL", fmt.Sprintf("%d", webPushTTL))
	req.Header.Set("Authorization", authHeader)
	req.Header.Set("Urgency", "normal")

	resp, err := p.client.Do(req)
	if err != nil {
		return p.failureResult(msg, 0, err.Error(), err,
			PushFailure{Kind: PushFailureUnknown, Reason: "transport error"})
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusOK {
		return &PushResult{UserID: msg.UserID, Success: true, StatusCode: resp.StatusCode}, nil
	}

	reason := readResponseReason(resp.Body)
	failure := ClassifyPushFailure(PlatformWeb, resp.StatusCode, reason)
	return &PushResult{
		UserID:     msg.UserID,
		Success:    false,
		StatusCode: resp.StatusCode,
		Reason:     reason,
		Error:      reason,
		Failure:    failure,
	}, fmt.Errorf("webpush error: %d %s", resp.StatusCode, reason)
}

func (p *WebPushProvider) failureResult(msg *PushMessage, statusCode int, reason string, err error, failure PushFailure) (*PushResult, error) {
	if reason == "" {
		reason = err.Error()
	}
	if failure.Reason == "" {
		failure.Reason = reason
	}
	return &PushResult{
		UserID:     msg.UserID,
		Success:    false,
		StatusCode: statusCode,
		Reason:     reason,
		Error:      reason,
		Failure:    failure,
	}, err
}

func readResponseReason(body io.Reader) string {
	data, _ := io.ReadAll(io.LimitReader(body, webPushBodyReadLimit))
	return strings.TrimSpace(string(data))
}

// parseWebPushSubscription 校验收到的订阅材料。
//
// p256dh 是 65 字节未压缩 P-256 公钥（首字节 0x04），auth 是 16 字节；
// 长度/前缀不对就直接失败，不要把非法点喂给 crypto/ecdh（会 panic 或返回无意义错误）。
func parseWebPushSubscription(msg *PushMessage) (endpoint string, uaPublic, authSecret []byte, err error) {
	endpoint = msg.Token
	if v := strings.TrimSpace(msg.Data[webPushKeyEndpoint]); v != "" {
		endpoint = v
	}
	u, parseErr := url.Parse(endpoint)
	if parseErr != nil || u.Scheme == "" || u.Host == "" {
		return "", nil, nil, fmt.Errorf("%w: 非法推送端点", ErrWebPushSubscriptionInvalid)
	}

	uaPublic, err = decodeBase64URL(msg.Data[webPushKeyP256DH])
	if err != nil || len(uaPublic) != 65 || uaPublic[0] != 0x04 {
		return "", nil, nil, fmt.Errorf("%w: p256dh 必须是 65 字节未压缩公钥", ErrWebPushSubscriptionInvalid)
	}
	authSecret, err = decodeBase64URL(msg.Data[webPushKeyAuth])
	if err != nil || len(authSecret) != 16 {
		return "", nil, nil, fmt.Errorf("%w: auth 必须是 16 字节", ErrWebPushSubscriptionInvalid)
	}
	return endpoint, uaPublic, authSecret, nil
}

func decodeBase64URL(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, errors.New("empty")
	}
	if b, err := base64.RawURLEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	return base64.URLEncoding.DecodeString(s)
}

// encryptWebPushPayload 实现 RFC 8291 的 aes128gcm 单记录加密。
//
// 密钥推导：
//
//	IKM  = HKDF(auth_secret, ECDH(客户端公钥, 服务端临时私钥), "WebPush: info\0" || ua_public || as_public)
//	CEK  = HKDF(salt, IKM, "Content-Encoding: aes128gcm\0")
//	nonce= HKDF(salt, IKM, "Content-Encoding: nonce\0")
//
// 输出结构：salt(16) || rs(4) || idlen(1) || as_public(65) || 密文。
// 临时密钥每帧重新生成：复用会让同一用户的多条通知共享 ECDH 密钥，弱化前向安全。
func encryptWebPushPayload(uaPublic, authSecret, plaintext []byte) ([]byte, error) {
	curve := ecdh.P256()
	asPrivate, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	uaKey, err := curve.NewPublicKey(uaPublic)
	if err != nil {
		return nil, err
	}
	shared, err := asPrivate.ECDH(uaKey)
	if err != nil {
		return nil, err
	}
	asPublic := asPrivate.PublicKey().Bytes()

	keyInfo := make([]byte, 0, len("WebPush: info\x00")+len(uaPublic)+len(asPublic))
	keyInfo = append(keyInfo, []byte("WebPush: info\x00")...)
	keyInfo = append(keyInfo, uaPublic...)
	keyInfo = append(keyInfo, asPublic...)

	ikm := hkdfSHA256(authSecret, shared, keyInfo, 32)
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	cek := hkdfSHA256(salt, ikm, []byte("Content-Encoding: aes128gcm\x00"), 16)
	nonce := hkdfSHA256(salt, ikm, []byte("Content-Encoding: nonce\x00"), 12)

	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	padded := make([]byte, 0, len(plaintext)+1)
	padded = append(padded, plaintext...)
	padded = append(padded, webPushPayloadDelimiter)
	ciphertext := gcm.Seal(nil, nonce, padded, nil)

	body := make([]byte, 0, 16+4+1+len(asPublic)+len(ciphertext))
	body = append(body, salt...)
	var recordSize [4]byte
	binary.BigEndian.PutUint32(recordSize[:], webPushRecordSize)
	body = append(body, recordSize[:]...)
	body = append(body, byte(len(asPublic)))
	body = append(body, asPublic...)
	body = append(body, ciphertext...)
	return body, nil
}

// hkdfSHA256 是 RFC 5869 的 extract-then-expand，固定 SHA-256。
// 只用到「一个密钥、一个 info」的简单组合，没必要引入额外依赖。
func hkdfSHA256(salt, ikm, info []byte, length int) []byte {
	extractor := hmac.New(sha256.New, salt)
	extractor.Write(ikm)
	prk := extractor.Sum(nil)

	var out []byte
	var previous []byte
	for counter := byte(1); len(out) < length; counter++ {
		expander := hmac.New(sha256.New, prk)
		expander.Write(previous)
		expander.Write(info)
		expander.Write([]byte{counter})
		previous = expander.Sum(nil)
		out = append(out, previous...)
	}
	return out[:length]
}

// vapidAuthorization 生成 `Authorization: vapid t=<JWT>,k=<公钥>`。
//
// aud 必须是订阅端点所在的 origin（协议 + 主机），用完整 URL 会被推送服务拒绝；
// JWT 用 ES256，k 是未压缩 P-256 公钥的 base64url 编码，浏览器据此验签。
func (p *WebPushProvider) vapidAuthorization(endpoint string) (string, error) {
	key, err := p.vapidKey()
	if err != nil {
		return "", err
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("webpush: 解析端点失败: %w", err)
	}

	claims := jwt.MapClaims{
		"aud": u.Scheme + "://" + u.Host,
		"exp": time.Now().Add(webPushVAPIDExpiry).Unix(),
		"sub": p.config.Subject,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
	signed, err := token.SignedString(key)
	if err != nil {
		return "", fmt.Errorf("webpush: VAPID 签名失败: %w", err)
	}

	publicKey := elliptic.Marshal(elliptic.P256(), key.X, key.Y)
	return "vapid t=" + signed + ",k=" + base64.RawURLEncoding.EncodeToString(publicKey), nil
}

func (p *WebPushProvider) vapidKey() (*ecdsa.PrivateKey, error) {
	p.keyOnce.Do(func() {
		p.key, p.keyErr = parseVAPIDPrivateKey(p.config.VAPIDPrivateKey)
	})
	return p.key, p.keyErr
}

// parseVAPIDPrivateKey 支持 PEM（PKCS#8 / SEC1）与 base64(url) 裸 DER 三种写法，
// 因为不同配置中心/环境变量对多行 PEM 的处理差异很大。
func parseVAPIDPrivateKey(raw string) (*ecdsa.PrivateKey, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, ErrWebPushNotConfigured
	}

	var der []byte
	if block, _ := pem.Decode([]byte(raw)); block != nil {
		der = block.Bytes
	} else if decoded, err := decodeBase64URL(raw); err == nil {
		der = decoded
	} else {
		return nil, fmt.Errorf("webpush: VAPID 私钥既不是 PEM 也不是 base64: %w", err)
	}

	if parsed, err := x509.ParsePKCS8PrivateKey(der); err == nil {
		key, ok := parsed.(*ecdsa.PrivateKey)
		if !ok {
			return nil, errors.New("webpush: VAPID 私钥不是 ECDSA 类型")
		}
		if key.Curve != elliptic.P256() {
			return nil, errors.New("webpush: VAPID 私钥必须是 P-256")
		}
		return key, nil
	}
	key, err := x509.ParseECPrivateKey(der)
	if err != nil {
		return nil, fmt.Errorf("webpush: 解析 VAPID 私钥失败: %w", err)
	}
	if key.Curve != elliptic.P256() {
		return nil, errors.New("webpush: VAPID 私钥必须是 P-256")
	}
	return key, nil
}
