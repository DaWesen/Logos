package push

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"Logos/pkg/logger"
	"Logos/pkg/obs"
)

type Platform string

const (
	PlatformIOS     Platform = "ios"
	PlatformAndroid Platform = "android"
	PlatformWeb     Platform = "web"
	PlatformDesktop Platform = "desktop"
)

const (
	// apnsExpirationWindow 离线设备上通知的保留时长。
	// APNs 的 apns-expiration 语义：设备离线时最多保留这么久，超时丢弃，
	// 避免用户上线后收到一堆「几小时前的消息提醒」。
	apnsExpirationWindow = time.Hour
	// DefaultSendConcurrency 单次 SendToUsers 同时进行的用户数上限。
	// 一个万人大群广播如果按用户起 goroutine，会瞬间打出上万并发 HTTP 请求，
	// 既打满本机连接数，也容易触发供应商的 QPS 限流（429 后整批失败）。
	DefaultSendConcurrency = 32
	// DefaultPerUserConcurrency 单个用户的设备并发上限。
	// 多设备用户常见 2~5 台，串行发送会把最坏延迟放大成「设备数 × 超时」。
	DefaultPerUserConcurrency = 4
)

type PushToken struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	DeviceID  string    `json:"device_id"`
	Platform  Platform  `json:"platform"`
	Token     string    `json:"token"`
	BundleID  string    `json:"bundle_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type PushMessage struct {
	ID       string            `json:"id"`
	UserID   string            `json:"user_id"`
	Token    string            `json:"token"`
	Platform Platform          `json:"platform"`
	Title    string            `json:"title"`
	Body     string            `json:"body"`
	Badge    int               `json:"badge,omitempty"`
	Sound    string            `json:"sound,omitempty"`
	Data     map[string]string `json:"data,omitempty"`
}

type PushResult struct {
	UserID  string `json:"user_id"`
	Success bool   `json:"success"`
	// StatusCode 供应商返回的 HTTP 状态码（网络层失败时为 0）。
	// 保留它而不是只拼错误字符串，是为了让失败分类（见 classify.go）能用上
	// 「410 一定代表令牌失效、429/5xx 一定可重试」这类权威信号。
	StatusCode int `json:"status_code,omitempty"`
	// Reason 供应商语义化原因（APNs 的 reason、FCM 的 results[].error）。
	Reason string `json:"reason,omitempty"`
	Error  string `json:"error,omitempty"`
	// Failure 失败分类，成功时为零值。调用方据此决定重试/注销令牌。
	Failure PushFailure `json:"-"`
}

type PushProvider interface {
	Name() string
	Send(ctx context.Context, msg *PushMessage) (*PushResult, error)
}

type APNSConfig struct {
	KeyID      string `json:"key_id"`
	TeamID     string `json:"team_id"`
	BundleID   string `json:"bundle_id"`
	KeyPath    string `json:"key_path"`
	Production bool   `json:"production"`
}

type APNSProvider struct {
	config APNSConfig
	client *http.Client
	// tokenSource 为 nil 时说明没有配置 .p8 私钥（本地 mock 或测试环境），
	// 此时不发送授权头，行为与历史版本一致。
	tokenSource *apnsTokenSource
}

func NewAPNSProvider(config APNSConfig) *APNSProvider {
	p := &APNSProvider{
		config: config,
		client: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{},
			},
		},
	}
	// 配置完整才启用鉴权；配置不全时保持不带头，避免启动期因为一个可选字段失败
	if config.KeyID != "" && config.TeamID != "" && config.KeyPath != "" {
		if source, err := NewAPNSTokenSource(config); err == nil {
			p.tokenSource = source
		} else {
			logger.Warn("APNs 鉴权初始化失败，将不带授权头发送（真实环境会被拒绝）",
				logger.ErrorField(err))
		}
	}
	return p
}

func (p *APNSProvider) Name() string { return "apns" }

func (p *APNSProvider) Send(ctx context.Context, msg *PushMessage) (*PushResult, error) {
	host := "https://api.sandbox.push.apple.com"
	if p.config.Production {
		host = "https://api.push.apple.com"
	}

	bundleID := p.config.BundleID
	if msg.Data != nil {
		if b, ok := msg.Data["bundle_id"]; ok && b != "" {
			bundleID = b
		}
	}

	payload := map[string]interface{}{
		"aps": map[string]interface{}{
			"alert": map[string]string{
				"title": msg.Title,
				"body":  msg.Body,
			},
			"badge": msg.Badge,
			"sound": msg.Sound,
		},
	}
	if len(msg.Data) > 0 {
		payload["data"] = msg.Data
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return &PushResult{UserID: msg.UserID, Success: false, Error: err.Error()}, err
	}

	url := fmt.Sprintf("%s/3/device/%s", host, msg.Token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return &PushResult{UserID: msg.UserID, Success: false, Error: err.Error()}, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("apns-topic", bundleID)
	req.Header.Set("apns-push-type", "alert")
	// 设备离线时通知最多保留 1 小时；超过后 Apple 直接丢弃，
	// 不设该头则按立即投递处理，离线设备会拿到过期通知。
	req.Header.Set("apns-expiration", strconv.FormatInt(time.Now().Add(apnsExpirationWindow).Unix(), 10))

	if p.tokenSource != nil {
		providerToken, tokenErr := p.tokenSource.Token()
		if tokenErr != nil {
			return &PushResult{UserID: msg.UserID, Success: false, Error: tokenErr.Error()}, tokenErr
		}
		req.Header.Set("Authorization", "Bearer "+providerToken)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return &PushResult{UserID: msg.UserID, Success: false, Error: err.Error()}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		return &PushResult{UserID: msg.UserID, Success: true}, nil
	}

	var errResp map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&errResp)
	reason := ""
	if r, ok := errResp["reason"]; ok {
		reason = fmt.Sprintf("%v", r)
	}

	failure := ClassifyPushFailure(PlatformIOS, resp.StatusCode, reason)
	return &PushResult{
		UserID:     msg.UserID,
		Success:    false,
		StatusCode: resp.StatusCode,
		Reason:     reason,
		Error:      reason,
		Failure:    failure,
	}, fmt.Errorf("APNs error: %d %s", resp.StatusCode, reason)
}

type FCMConfig struct {
	ServerKey string `json:"server_key"`
	ProjectID string `json:"project_id"`
}

type FCMProvider struct {
	config FCMConfig
	client *http.Client
}

func NewFCMProvider(config FCMConfig) *FCMProvider {
	return &FCMProvider{
		config: config,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

func (p *FCMProvider) Name() string { return "fcm" }

func (p *FCMProvider) Send(ctx context.Context, msg *PushMessage) (*PushResult, error) {
	payload := map[string]interface{}{
		"to": msg.Token,
		"notification": map[string]string{
			"title": msg.Title,
			"body":  msg.Body,
		},
		"data":     msg.Data,
		"priority": "high",
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return &PushResult{UserID: msg.UserID, Success: false, Error: err.Error()}, err
	}

	url := "https://fcm.googleapis.com/fcm/send"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return &PushResult{UserID: msg.UserID, Success: false, Error: err.Error()}, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "key="+p.config.ServerKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return &PushResult{UserID: msg.UserID, Success: false, Error: err.Error()}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		// FCM 老接口（/fcm/send）即使单条失败也返回 200，真正的失败原因在
		// results[0].error 里（NotRegistered / InvalidRegistration / Unavailable …）。
		// 原实现只读顶层 success 字段，拿不到原因 → 无法区分「可重试」与「令牌已失效」。
		var body struct {
			Success int `json:"success"`
			Failure int `json:"failure"`
			Results []struct {
				Error string `json:"error"`
			} `json:"results"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&body)

		reason := ""
		if len(body.Results) > 0 {
			reason = body.Results[0].Error
		}
		if body.Success > 0 && reason == "" {
			return &PushResult{UserID: msg.UserID, Success: true}, nil
		}
		if reason == "" {
			reason = "message rejected"
		}

		failure := ClassifyPushFailure(PlatformAndroid, resp.StatusCode, reason)
		return &PushResult{
			UserID:     msg.UserID,
			Success:    false,
			StatusCode: resp.StatusCode,
			Reason:     reason,
			Error:      reason,
			Failure:    failure,
		}, fmt.Errorf("FCM rejected: %s", reason)
	}

	failure := ClassifyPushFailure(PlatformAndroid, resp.StatusCode, resp.Status)
	return &PushResult{
		UserID:     msg.UserID,
		Success:    false,
		StatusCode: resp.StatusCode,
		Reason:     resp.Status,
		Error:      resp.Status,
		Failure:    failure,
	}, fmt.Errorf("FCM error: %s", resp.Status)
}

type WebPushConfig struct {
	VAPIDPublicKey  string `json:"vapid_public_key"`
	VAPIDPrivateKey string `json:"vapid_private_key"`
	Subject         string `json:"subject"`
}

type WebPushProvider struct {
	config WebPushConfig
	client *http.Client
}

func NewWebPushProvider(config WebPushConfig) *WebPushProvider {
	return &WebPushProvider{
		config: config,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

func (p *WebPushProvider) Name() string { return "webpush" }

// ErrWebPushNotImplemented 表示 Web Push 通道尚未接入真实发送实现。
var ErrWebPushNotImplemented = errors.New("webpush provider not implemented")

// Send 当前是未实现的占位：明确返回失败，而不是伪装成功。
//
// 原实现直接返回 Success=true，调用方会把它计入发送成功、不再重试，
// 而设备一条通知也收不到——这是最难排查的一类「静默丢消息」。
// 归类为 permanent（重试也不会成功），但 TokenInvalid=false：令牌本身可能有效，
// 只是服务端没实现，绝不能因此把用户设备注销掉。
func (p *WebPushProvider) Send(ctx context.Context, msg *PushMessage) (*PushResult, error) {
	reason := "webpush provider not implemented"
	return &PushResult{
		UserID:  msg.UserID,
		Success: false,
		Reason:  reason,
		Error:   reason,
		Failure: PushFailure{
			Kind:         PushFailurePermanent,
			Reason:       reason,
			TokenInvalid: false,
		},
	}, ErrWebPushNotImplemented
}

type PushManager struct {
	providers map[Platform]PushProvider
	tokens    map[string][]*PushToken
	mu        sync.RWMutex

	// 并发控制：早期实现「每个用户一个 goroutine、每个用户内设备串行」，
	// 大群广播时 goroutine 数量与用户数成正比（无上限），而单用户多设备时
	// 最坏延迟 = 设备数 × (10s 超时)。现在两层都上限：
	//   - sendSem 限制跨用户的并发数（全局背压）；
	//   - perUserConcurrency 限制单用户设备并发（顺序不再是瓶颈）。
	sendConcurrency    int
	perUserConcurrency int
	sendSem            chan struct{}
}

var pushManager *PushManager
var pushOnce sync.Once

func GetPushManager() *PushManager {
	pushOnce.Do(func() {
		pushManager = &PushManager{
			providers:          make(map[Platform]PushProvider),
			tokens:             make(map[string][]*PushToken),
			sendConcurrency:    DefaultSendConcurrency,
			perUserConcurrency: DefaultPerUserConcurrency,
		}
	})
	return pushManager
}

// SetSendConcurrency 调整跨用户并发上限（<=0 回退默认值）。仅允许在大量测试/压测中调整。
func (m *PushManager) SetSendConcurrency(n int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if n <= 0 {
		n = DefaultSendConcurrency
	}
	m.sendConcurrency = n
	// 已创建的信号量尺寸不可变，置空让下次调用按新值重建
	m.sendSem = nil
}

// SetPerUserConcurrency 调整单用户设备并发上限（<=0 回退默认值）。
func (m *PushManager) SetPerUserConcurrency(n int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if n <= 0 {
		n = DefaultPerUserConcurrency
	}
	m.perUserConcurrency = n
}

func (m *PushManager) sendSemaphore() chan struct{} {
	m.mu.RLock()
	if m.sendSem != nil {
		ch := m.sendSem
		m.mu.RUnlock()
		return ch
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sendSem == nil {
		n := m.sendConcurrency
		if n <= 0 {
			n = DefaultSendConcurrency
		}
		m.sendSem = make(chan struct{}, n)
	}
	return m.sendSem
}

func (m *PushManager) perUserLimit() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.perUserConcurrency <= 0 {
		return DefaultPerUserConcurrency
	}
	return m.perUserConcurrency
}

// providerFor 读 map 必须加锁：RegisterProvider 是运行期写入（HTTP 注册接口），
// 而 SendToUser 在消费线程并发读取，不加锁就是 map 并发读写（可能直接 panic）。
func (m *PushManager) providerFor(platform Platform) (PushProvider, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.providers[platform]
	return p, ok
}

func (m *PushManager) RegisterProvider(platform Platform, provider PushProvider) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.providers[platform] = provider
	logger.Info("推送提供者已注册",
		logger.StringField("platform", string(platform)),
		logger.StringField("provider", provider.Name()))
}

func (m *PushManager) RegisterToken(token *PushToken) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i, t := range m.tokens[token.UserID] {
		if t.DeviceID == token.DeviceID {
			m.tokens[token.UserID][i] = token
			return
		}
	}
	m.tokens[token.UserID] = append(m.tokens[token.UserID], token)
}

func (m *PushManager) UnregisterToken(userID, deviceID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	tokens := m.tokens[userID]
	for i, t := range tokens {
		if t.DeviceID == deviceID {
			tokens = append(tokens[:i], tokens[i+1:]...)
			if len(tokens) == 0 {
				// 空切片留在 map 里会让 GetUserTokens 每次都走一次分配，
				// 且用户维度 key 永不回收，删除整条记录。
				delete(m.tokens, userID)
			} else {
				m.tokens[userID] = tokens
			}
			return
		}
	}
}

func (m *PushManager) GetUserTokens(userID string) []*PushToken {
	m.mu.RLock()
	defer m.mu.RUnlock()
	tokens := m.tokens[userID]
	result := make([]*PushToken, len(tokens))
	copy(result, tokens)
	return result
}

// dedupePushUserIDs 保序去重并剔除空 ID。
// 大群广播的接收者名单常带重复（发送者被补进名单、多节点合并），
// 去重能让后续信号量与 goroutine 数量不被无谓放大。
func dedupePushUserIDs(userIDs []string) []string {
	if len(userIDs) < 2 {
		if len(userIDs) == 1 && userIDs[0] == "" {
			return nil
		}
		return userIDs
	}
	seen := make(map[string]struct{}, len(userIDs))
	unique := make([]string, 0, len(userIDs))
	for _, id := range userIDs {
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	if len(unique) == len(userIDs) {
		return userIDs
	}
	return unique
}

// SendToUser 向一个用户的所有设备推送，返回与设备列表同序的结果。
//
// 设备之间并发发送（受 perUserConcurrency 限制）：串行时最坏延迟是
// 「设备数 × 单次超时」，5 台设备 × 10s 会让一次离线提醒拖到 50s。
// 结果按原设备下标落位，调用方可以稳定地按 token 对账。
//
// 供应商判定「设备令牌已失效」（APNs 410 / FCM NotRegistered）时，
// 本轮立即注销该 token：继续保留只会让后续每条推送都为死 token 打一次外部请求。
func (m *PushManager) SendToUser(ctx context.Context, userID, title, body string, data map[string]string) []PushResult {
	tokens := m.GetUserTokens(userID)
	if len(tokens) == 0 {
		return nil
	}

	results := make([]PushResult, len(tokens))
	perUser := m.perUserLimit()
	sem := make(chan struct{}, perUser)

	var wg sync.WaitGroup
	for i, token := range tokens {
		provider, ok := m.providerFor(token.Platform)
		if !ok {
			results[i] = PushResult{
				UserID:  userID,
				Success: false,
				Reason:  fmt.Sprintf("no provider for platform %s", token.Platform),
				Error:   fmt.Sprintf("no provider for platform %s", token.Platform),
				Failure: PushFailure{Kind: PushFailureUnknown, Reason: "no provider registered"},
			}
			obs.RecordPushSend(ctx, string(token.Platform), "skipped")
			continue
		}

		// 拿到槽位前先看 ctx：请求已取消时不必再排队，也不该再发外部请求。
		// 单独判断而不是只靠 select，是因为信号量有空格时 select 两个分支都就绪，
		// 随机选中槽位会让「取消后是否仍发请求」变得不确定。
		if err := ctx.Err(); err != nil {
			results[i] = PushResult{
				UserID:  userID,
				Success: false,
				Reason:  err.Error(),
				Error:   err.Error(),
				Failure: PushFailure{Kind: PushFailureUnknown, Reason: "context canceled"},
			}
			obs.RecordPushSend(ctx, string(token.Platform), "skipped")
			continue
		}

		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			results[i] = PushResult{
				UserID:  userID,
				Success: false,
				Reason:  ctx.Err().Error(),
				Error:   ctx.Err().Error(),
				Failure: PushFailure{Kind: PushFailureUnknown, Reason: "context canceled"},
			}
			obs.RecordPushSend(ctx, string(token.Platform), "skipped")
			continue
		}

		idx, tk, pv := i, token, provider
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			msg := &PushMessage{
				ID:       "push_" + strconv.FormatInt(time.Now().UnixNano(), 10),
				UserID:   userID,
				Token:    tk.Token,
				Platform: tk.Platform,
				Title:    title,
				Body:     body,
				Badge:    1,
				Sound:    "default",
				Data:     data,
			}

			result, err := pv.Send(ctx, msg)
			if result == nil {
				// 供应商实现可能只返回 error；补一个结构化结果，避免调用方拿到 nil 无法对账
				if err == nil {
					result = &PushResult{UserID: userID, Success: true}
				} else {
					result = &PushResult{UserID: userID, Success: false, Error: err.Error()}
				}
			}
			if err != nil {
				logger.Warn("推送发送失败",
					logger.StringField("user_id", userID),
					logger.StringField("platform", string(tk.Platform)),
					logger.ErrorField(err))
			}

			if !result.Success && result.Failure.Kind == "" {
				result.Failure = ClassifyPushFailure(tk.Platform, result.StatusCode, result.Reason)
			}
			obs.RecordPushSend(ctx, string(tk.Platform), pushOutcome(*result))

			if result.Failure.ShouldUnregister() {
				m.UnregisterToken(userID, tk.DeviceID)
				obs.RecordPushTokenEvicted(ctx, string(tk.Platform))
				logger.Info("推送令牌已失效，已注销",
					logger.StringField("user_id", userID),
					logger.StringField("platform", string(tk.Platform)),
					logger.StringField("reason", result.Failure.Reason))
			}

			results[idx] = *result
		}()
	}
	wg.Wait()

	return results
}

// pushOutcome 把结果压缩成受控的指标标签（避免把供应商 reason 当标签造成高基数）。
func pushOutcome(r PushResult) string {
	if r.Success {
		return "sent"
	}
	switch r.Failure.Kind {
	case PushFailureRetryable:
		return "failed_retryable"
	case PushFailurePermanent:
		return "failed_permanent"
	default:
		return "failed_unknown"
	}
}

// SendToUsers 并发推送给多个用户，跨用户并发受 sendSem 限制。
//
// 原实现按用户无上限地起 goroutine：万人大群广播会瞬间打出上万并发 HTTP 请求，
// 触发供应商 QPS 限流（429），而限流又会让更多请求进入重试，形成放大。
// 现在用全局信号量做背压，超出并发的用户排队等待，ctx 取消时提前停止排队。
func (m *PushManager) SendToUsers(ctx context.Context, userIDs []string, title, body string, data map[string]string) {
	userIDs = dedupePushUserIDs(userIDs)
	if len(userIDs) == 0 {
		return
	}

	sem := m.sendSemaphore()
	var wg sync.WaitGroup
	for _, uid := range userIDs {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			wg.Wait()
			return
		}

		wg.Add(1)
		go func(userID string) {
			defer wg.Done()
			defer func() { <-sem }()
			m.SendToUser(ctx, userID, title, body, data)
		}(uid)
	}
	wg.Wait()
}

func (m *PushManager) IsConfigured() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.providers) > 0
}

func DetectPlatform(userAgent string) Platform {
	ua := strings.ToLower(userAgent)
	if strings.Contains(ua, "iphone") || strings.Contains(ua, "ipad") || strings.Contains(ua, "ios") {
		return PlatformIOS
	}
	if strings.Contains(ua, "android") {
		return PlatformAndroid
	}
	if strings.Contains(ua, "electron") || strings.Contains(ua, "desktop") {
		return PlatformDesktop
	}
	return PlatformWeb
}
