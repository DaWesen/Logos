package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"Logos/pkg/client"
)

type BotClientAdapter struct {
	*client.BotClient
}

func NewBotClientAdapter(c *client.BotClient) *BotClientAdapter {
	return &BotClientAdapter{BotClient: c}
}

func (a *BotClientAdapter) SendBotMessage(ctx context.Context, botID, content, userID, chatID string, modelConfig map[string]string) (string, error) {
	result, err := a.BotClient.SendBotMessage(ctx, botID, content, userID, chatID, false, modelConfig)
	if err != nil {
		return "", err
	}
	return result.Content, nil
}

type ModerationClientAdapter struct {
	*client.ModerationClient
}

func NewModerationClientAdapter(c *client.ModerationClient) *ModerationClientAdapter {
	return &ModerationClientAdapter{ModerationClient: c}
}

func (a *ModerationClientAdapter) ModerateContent(ctx context.Context, content, contentID, contentType string) (bool, error) {
	result, err := a.ModerationClient.ModerateContent(ctx, content, contentID, contentType)
	if err != nil {
		return false, err
	}

	const moderationResultRejected = 3
	return result.Result == moderationResultRejected, nil
}

type TranslationClient interface {
	Translate(ctx context.Context, content, sourceLang, targetLang, contentID string) (string, error)
}

type TranslationClientAdapter struct {
	*client.ModerationClient
}

func NewTranslationClientAdapter(c *client.ModerationClient) *TranslationClientAdapter {
	return &TranslationClientAdapter{ModerationClient: c}
}

func (a *TranslationClientAdapter) Translate(ctx context.Context, content, sourceLang, targetLang, contentID string) (string, error) {
	result, err := a.ModerationClient.Translate(ctx, content, sourceLang, targetLang, contentID)
	if err != nil {
		return "", err
	}
	return result, nil
}

type friendshipCacheEntry struct {
	isFriend  bool
	isBlocked bool
	cachedAt  time.Time
}

type ContactCheckerAdapter struct {
	*client.ContactClient
	cache map[string]*friendshipCacheEntry
	mu    sync.RWMutex
	ttl   time.Duration
}

func NewContactCheckerAdapter(c *client.ContactClient) *ContactCheckerAdapter {
	return &ContactCheckerAdapter{
		ContactClient: c,
		cache:         make(map[string]*friendshipCacheEntry),
		ttl:           5 * time.Minute,
	}
}

func (a *ContactCheckerAdapter) cacheKey(userID, friendID string) string {
	return fmt.Sprintf("%s:%s", userID, friendID)
}

func (a *ContactCheckerAdapter) getCached(key string) (*friendshipCacheEntry, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	entry, ok := a.cache[key]
	if !ok {
		return nil, false
	}
	if time.Since(entry.cachedAt) > a.ttl {
		return nil, false
	}
	return entry, true
}

func (a *ContactCheckerAdapter) setCache(key string, isFriend, isBlocked bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cache[key] = &friendshipCacheEntry{
		isFriend:  isFriend,
		isBlocked: isBlocked,
		cachedAt:  time.Now(),
	}
}

type FriendshipResult struct {
	IsFriend  bool
	IsBlocked bool
	Cached    bool
}

func (a *ContactCheckerAdapter) CheckFriendship(ctx context.Context, userID, friendID string) (*FriendshipResult, error) {
	key := a.cacheKey(userID, friendID)

	if cached, ok := a.getCached(key); ok {
		return &FriendshipResult{
			IsFriend:  cached.isFriend,
			IsBlocked: cached.isBlocked,
			Cached:    true,
		}, nil
	}

	status, err := a.ContactClient.IsFriend(ctx, userID, friendID)
	if err != nil {
		if cached, ok := a.getCachedExpired(key); ok {
			return &FriendshipResult{
				IsFriend:  cached.isFriend,
				IsBlocked: cached.isBlocked,
				Cached:    true,
			}, nil
		}
		return nil, err
	}

	a.setCache(key, status.IsFriend, status.IsBlocked)

	return &FriendshipResult{
		IsFriend:  status.IsFriend,
		IsBlocked: status.IsBlocked,
		Cached:    false,
	}, nil
}

func (a *ContactCheckerAdapter) getCachedExpired(key string) (*friendshipCacheEntry, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	entry, ok := a.cache[key]
	if !ok {
		return nil, false
	}
	return entry, true
}

func (a *ContactCheckerAdapter) IsFriend(ctx context.Context, userID, friendID string) (bool, error) {
	result, err := a.CheckFriendship(ctx, userID, friendID)
	if err != nil {
		return false, err
	}
	return result.IsFriend, nil
}

func (a *ContactCheckerAdapter) IsBlocked(ctx context.Context, userID, friendID string) (bool, error) {
	result, err := a.CheckFriendship(ctx, userID, friendID)
	if err != nil {
		return false, err
	}
	return result.IsBlocked, nil
}
