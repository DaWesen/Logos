package gateway

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	"Logos/internal/service/messaging/types"
	"Logos/pkg/client"
	"Logos/pkg/logger"
)

const (
	// senderInfoTTL 发送者资料多久刷新一次。昵称/头像属于低频变更数据，
	// 一次 RPC 的结果可服务 5 分钟内的全部消息。
	senderInfoTTL = 5 * time.Minute
	// senderInfoNegativeTTL 查询失败后的短暂负缓存，避免下游故障时每条消息都重试一次。
	senderInfoNegativeTTL = 30 * time.Second
	// senderInfoMaxEntries 缓存条目上限，超过后先清过期项，仍超限则整体清空。
	// 只为兜底内存占用，不做 LRU——这里的目标是「别让缓存无限长大」，不是命中率最优。
	senderInfoMaxEntries = 20000
)

// UserInfoSource 只依赖「按用户 ID 查资料」这一个能力，便于单测注入假实现。
// *client.UserClient 满足该接口。
type UserInfoSource interface {
	GetUserInfo(ctx context.Context, userID int64) (*client.UserInfo, error)
}

type senderInfo struct {
	name   string
	avatar string
}

type senderInfoEntry struct {
	info     senderInfo
	expireAt time.Time
}

// SenderEnricher 为下行消息补全发送者昵称与头像，带 TTL 缓存。
//
// 下行协议统一后 WS 与 TCP 都走同一份数据，补全只做一次；
// 原实现对每条 Kafka 消息同步调用一次用户服务，下游抖动会直接拖慢消费（积压 → 重试 → 放大），
// 且同一个人连发 N 条消息就查 N 次。缓存后高频发送者只按 TTL 刷新。
type SenderEnricher struct {
	source      UserInfoSource
	ttl         time.Duration
	negativeTTL time.Duration
	maxEntries  int
	now         func() time.Time

	mu    sync.Mutex
	cache map[string]senderInfoEntry
}

func NewSenderEnricher(source UserInfoSource) *SenderEnricher {
	return &SenderEnricher{
		source:      source,
		ttl:         senderInfoTTL,
		negativeTTL: senderInfoNegativeTTL,
		maxEntries:  senderInfoMaxEntries,
		now:         time.Now,
		cache:       make(map[string]senderInfoEntry),
	}
}

// Enrich 补全事件的发送者昵称与头像；缺失的字段才填，不覆盖上游已给出的值。
// bot_ / system / 非数字 ID 属于系统身份，不做用户查询。
func (e *SenderEnricher) Enrich(ctx context.Context, event *types.MessageEvent) {
	if e == nil || e.source == nil || event == nil {
		return
	}
	if event.SenderName != "" && event.SenderAvatar != "" {
		return
	}

	senderID := event.SenderID
	if senderID == "" || senderID == "system" || strings.HasPrefix(senderID, "bot_") {
		return
	}
	uid, err := strconv.ParseInt(senderID, 10, 64)
	if err != nil {
		return
	}

	info, ok := e.lookup(senderID)
	if !ok {
		user, err := e.source.GetUserInfo(ctx, uid)
		if err != nil {
			logger.Warn("补全发送者信息失败",
				logger.StringField("sender_id", senderID),
				logger.ErrorField(err))
			// 负缓存：下游故障期间不要每条消息都打一次 RPC
			e.store(senderID, senderInfo{}, e.negativeTTL)
			return
		}
		if user == nil {
			e.store(senderID, senderInfo{}, e.negativeTTL)
			return
		}
		info = senderInfo{name: user.Username, avatar: user.Avatar}
		e.store(senderID, info, e.ttl)
	}

	if event.SenderName == "" {
		event.SenderName = info.name
	}
	if event.SenderAvatar == "" && info.avatar != "" {
		event.SenderAvatar = info.avatar
	}
}

func (e *SenderEnricher) lookup(senderID string) (senderInfo, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()

	entry, ok := e.cache[senderID]
	if !ok {
		return senderInfo{}, false
	}
	if e.now().Before(entry.expireAt) {
		return entry.info, true
	}

	delete(e.cache, senderID)
	return senderInfo{}, false
}

func (e *SenderEnricher) store(senderID string, info senderInfo, ttl time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if _, exists := e.cache[senderID]; !exists && len(e.cache) >= e.maxEntries {
		e.evict()
		// 清完过期项仍满：整体丢弃，保证缓存有界（下次访问自然重建）
		if len(e.cache) >= e.maxEntries {
			e.cache = make(map[string]senderInfoEntry)
		}
	}
	e.cache[senderID] = senderInfoEntry{info: info, expireAt: e.now().Add(ttl)}
}

// evict 清理已过期条目；调用方需持有 e.mu。
func (e *SenderEnricher) evict() {
	now := e.now()
	for senderID, entry := range e.cache {
		if now.Before(entry.expireAt) {
			continue
		}
		delete(e.cache, senderID)
	}
}
