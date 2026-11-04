package qqbridge

import (
	"context"
	"fmt"
	"time"

	botmodel "Logos/internal/service/ai/bot/model"

	"gorm.io/gorm"
)

// BotBindingLookup 用 GORM 实现 converter 的回源接口。
// 只查单条绑定，走 QQ 号唯一索引，避免每次消息都扫表。
type BotBindingLookup struct {
	DB *gorm.DB
}

// LookupBotByQQ 返回绑定到该 QQ 号的 Bot ID；未绑定返回空字符串。
func (l BotBindingLookup) LookupBotByQQ(ctx context.Context, qqNumber string) (string, error) {
	if l.DB == nil || qqNumber == "" {
		return "", nil
	}
	var botID string
	err := l.DB.WithContext(ctx).
		Model(&botmodel.Bot{}).
		Select("id").
		Where("qq_number = ?", qqNumber).
		Limit(1).
		Scan(&botID).Error
	if err != nil {
		return "", fmt.Errorf("查询 QQ-Bot 绑定失败: %w", err)
	}
	return botID, nil
}

// BotBinding QQ 号与 Bot ID 的绑定。
type BotBinding struct {
	QQNumber string
	BotID    string
}

// bindingCache 只依赖写缓存能力，测试不需要起 Redis。
type bindingCache interface {
	Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error
}

// LoadBotBindings 从 Bot 表读取所有非空的 QQ 绑定。
func LoadBotBindings(ctx context.Context, db *gorm.DB) ([]BotBinding, error) {
	var rows []struct {
		ID       string
		QQNumber string
	}
	err := db.WithContext(ctx).
		Model(&botmodel.Bot{}).
		Select("id", "qq_number").
		Where("qq_number <> ''").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("读取 Bot QQ 绑定失败: %w", err)
	}

	bindings := make([]BotBinding, 0, len(rows))
	for _, row := range rows {
		if row.ID == "" || row.QQNumber == "" {
			continue
		}
		bindings = append(bindings, BotBinding{QQNumber: row.QQNumber, BotID: row.ID})
	}
	return bindings, nil
}

// WarmupBotBindings 把绑定写回 Redis。
//
// Bot 创建/更新时会写缓存，但 Redis 重启、被逐出或换实例后这些 key 会丢；
// 丢了之后入站 QQ 消息会退化成 qq_<号码> 用户，私聊也不会进入 Bot 会话。
// 单个 key 写失败不阻断其余绑定，返回成功数量与第一个错误。
func WarmupBotBindings(ctx context.Context, bindings []BotBinding, cache bindingCache) (int, error) {
	if cache == nil {
		return 0, fmt.Errorf("绑定缓存不可用")
	}

	written := 0
	var firstErr error
	for _, binding := range bindings {
		if binding.QQNumber == "" || binding.BotID == "" {
			continue
		}
		key := "qq:bot_bind:" + binding.QQNumber
		if err := cache.Set(ctx, key, binding.BotID, 0); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("写入 %s 失败: %w", key, err)
			}
			continue
		}
		written++
	}
	return written, firstErr
}
