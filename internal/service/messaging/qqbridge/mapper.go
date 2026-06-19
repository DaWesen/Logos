package qqbridge

import (
	"context"
	"fmt"
	"time"

	"Logos/config"
	"Logos/pkg/cache"
	"Logos/pkg/logger"
)

// Mapper 用户/群组映射器
type Mapper struct {
	cfg       *config.Config
	redis     cache.Cache
	qqPrefix  string
	grpPrefix string
}

// NewMapper 创建映射器
func NewMapper(cfg *config.Config) *Mapper {
	qqPrefix := cfg.QQBridge.QQUserPrefix
	if qqPrefix == "" {
		qqPrefix = "qq_"
	}
	grpPrefix := cfg.QQBridge.QQGroupPrefix
	if grpPrefix == "" {
		grpPrefix = "qqgroup_"
	}

	return &Mapper{
		cfg:       cfg,
		redis:     cache.NewRedisCache(),
		qqPrefix:  qqPrefix,
		grpPrefix: grpPrefix,
	}
}

// GetOrCreateUser 获取或创建 QQ 用户映射
func (m *Mapper) GetOrCreateUser(ctx context.Context, qqNumber int64, nickname string) (string, error) {
	qqStr := fmt.Sprintf("%d", qqNumber)
	logosID := fmt.Sprintf("%s%s", m.qqPrefix, qqStr)

	key := fmt.Sprintf("qq:user:%s", qqStr)
	exists, err := m.redis.Exists(ctx, key)
	if err != nil {
		logger.Warn("检查QQ用户映射失败", logger.ErrorField(err))
		return logosID, nil
	}

	if !exists {
		// 创建新映射
		fields := map[string]interface{}{
			"logos_id":    logosID,
			"qq_number":   qqStr,
			"qq_nickname": nickname,
			"last_sync":   time.Now().Format(time.RFC3339),
		}
		if err := m.redis.HMSet(ctx, key, fields); err != nil {
			logger.Warn("保存QQ用户映射失败", logger.ErrorField(err))
		}
		// 设置过期时间
		if err := m.redis.Expire(ctx, key, 24*time.Hour); err != nil {
			logger.Warn("设置QQ用户映射过期时间失败", logger.ErrorField(err))
		}

		// 反向映射
		reverseKey := fmt.Sprintf("qq:reverse:%s", logosID)
		if err := m.redis.Set(ctx, reverseKey, qqStr, 24*time.Hour); err != nil {
			logger.Warn("保存QQ反向映射失败", logger.ErrorField(err))
		}
	}

	return logosID, nil
}

// GetOrCreateGroup 获取或创建 QQ 群组映射
func (m *Mapper) GetOrCreateGroup(ctx context.Context, qqGroupNumber int64, groupName string) (string, error) {
	qqStr := fmt.Sprintf("%d", qqGroupNumber)
	logosID := fmt.Sprintf("%s%s", m.grpPrefix, qqStr)

	key := fmt.Sprintf("qq:group:%s", qqStr)
	exists, err := m.redis.Exists(ctx, key)
	if err != nil {
		logger.Warn("检查QQ群映射失败", logger.ErrorField(err))
		return logosID, nil
	}

	if !exists {
		fields := map[string]interface{}{
			"logos_group_id": logosID,
			"group_name":     groupName,
			"last_sync":      time.Now().Format(time.RFC3339),
		}
		if err := m.redis.HMSet(ctx, key, fields); err != nil {
			logger.Warn("保存QQ群映射失败", logger.ErrorField(err))
		}
		// 设置过期时间
		if err := m.redis.Expire(ctx, key, 24*time.Hour); err != nil {
			logger.Warn("设置QQ群映射过期时间失败", logger.ErrorField(err))
		}
	}

	return logosID, nil
}

// GetQQNumberByLogosID 根据 Logos ID 获取 QQ 号
func (m *Mapper) GetQQNumberByLogosID(ctx context.Context, logosID string) (string, error) {
	reverseKey := fmt.Sprintf("qq:reverse:%s", logosID)
	qqNumber, err := m.redis.Get(ctx, reverseKey)
	if err != nil {
		return "", err
	}
	return qqNumber, nil
}
