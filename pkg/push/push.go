package push

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"Logos/pkg/logger"
)

type Platform string

const (
	PlatformIOS     Platform = "ios"
	PlatformAndroid Platform = "android"
	PlatformWeb     Platform = "web"
	PlatformDesktop Platform = "desktop"
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
	Error   string `json:"error,omitempty"`
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
}

func NewAPNSProvider(config APNSConfig) *APNSProvider {
	return &APNSProvider{
		config: config,
		client: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{},
			},
		},
	}
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

	return &PushResult{UserID: msg.UserID, Success: false, Error: reason}, fmt.Errorf("APNs error: %s", reason)
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
		var result map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&result)
		if success, ok := result["success"].(float64); ok && success > 0 {
			return &PushResult{UserID: msg.UserID, Success: true}, nil
		}
		return &PushResult{UserID: msg.UserID, Success: false, Error: "FCM rejected"}, fmt.Errorf("FCM rejected")
	}

	return &PushResult{UserID: msg.UserID, Success: false, Error: resp.Status}, fmt.Errorf("FCM error: %s", resp.Status)
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

func (p *WebPushProvider) Send(ctx context.Context, msg *PushMessage) (*PushResult, error) {
	return &PushResult{UserID: msg.UserID, Success: true}, nil
}

type PushManager struct {
	providers map[Platform]PushProvider
	tokens    map[string][]*PushToken
	mu        sync.RWMutex
}

var pushManager *PushManager
var pushOnce sync.Once

func GetPushManager() *PushManager {
	pushOnce.Do(func() {
		pushManager = &PushManager{
			providers: make(map[Platform]PushProvider),
			tokens:    make(map[string][]*PushToken),
		}
	})
	return pushManager
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
			m.tokens[userID] = append(tokens[:i], tokens[i+1:]...)
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

func (m *PushManager) SendToUser(ctx context.Context, userID, title, body string, data map[string]string) []PushResult {
	tokens := m.GetUserTokens(userID)
	if len(tokens) == 0 {
		return nil
	}

	var results []PushResult
	for _, token := range tokens {
		provider, ok := m.providers[token.Platform]
		if !ok {
			results = append(results, PushResult{
				UserID:  userID,
				Success: false,
				Error:   fmt.Sprintf("no provider for platform %s", token.Platform),
			})
			continue
		}

		msg := &PushMessage{
			ID:       "push_" + strconv.FormatInt(time.Now().UnixNano(), 10),
			UserID:   userID,
			Token:    token.Token,
			Platform: token.Platform,
			Title:    title,
			Body:     body,
			Badge:    1,
			Sound:    "default",
			Data:     data,
		}

		result, err := provider.Send(ctx, msg)
		if err != nil {
			logger.Warn("推送发送失败",
				logger.StringField("user_id", userID),
				logger.StringField("platform", string(token.Platform)),
				logger.ErrorField(err))
		}
		if result != nil {
			results = append(results, *result)
		}
	}

	return results
}

func (m *PushManager) SendToUsers(ctx context.Context, userIDs []string, title, body string, data map[string]string) {
	var wg sync.WaitGroup
	for _, uid := range userIDs {
		wg.Add(1)
		go func(userID string) {
			defer wg.Done()
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
