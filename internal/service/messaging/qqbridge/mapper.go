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
	cfg   *config.Config
	redis cache.Cache
	// redisFactory 允许测试注入内存实现；nil 时按需调用 cache.NewRedisCache。
	redisFactory func() cache.Cache
	qqPrefix     string
	grpPrefix    string
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
		qqPrefix:  qqPrefix,
		grpPrefix: grpPrefix,
	}
}

// getCache 每次操作都重新取实例，而不是在构造时缓存 nil。
//
// NewRedisCache 在 Redis 不可用时返回 nil；如果这里在 NewMapper 时缓存下来，
// 那么即使 Redis 之后恢复（NewRedisCache 已按冷却期重试成功），
// 这个 Mapper 也会在整个进程生命周期内一直拿到 nil，用户/群映射永远写不进去。
func (m *Mapper) getCache() cache.Cache {
	if m.redisFactory != nil {
		return m.redisFactory()
	}
	if m.redis != nil {
		return m.redis
	}
	return cache.NewRedisCache()
}

// GetOrCreateUser 获取或创建 QQ 用户映射
func (m *Mapper) GetOrCreateUser(ctx context.Context, qqNumber int64, nickname string) (string, error) {
	qqStr := fmt.Sprintf("%d", qqNumber)
	logosID := fmt.Sprintf("%s%s", m.qqPrefix, qqStr)

	key := fmt.Sprintf("qq:user:%s", qqStr)
	redis := m.getCache()
	if redis == nil {
		// 缓存不可用时仍然返回确定性的 logosID：映射是「可重建」的派生数据，
		// 不能因为缓存故障让整条 QQ 消息失败。
		logger.Debug("Redis不可用，QQ用户映射降级为确定性ID",
			logger.StringField("qq_number", qqStr))
		return logosID, nil
	}

	exists, err := redis.Exists(ctx, key)
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
		if err := redis.HMSet(ctx, key, fields); err != nil {
			logger.Warn("保存QQ用户映射失败", logger.ErrorField(err))
		}
		// 设置过期时间
		if err := redis.Expire(ctx, key, 24*time.Hour); err != nil {
			logger.Warn("设置QQ用户映射过期时间失败", logger.ErrorField(err))
		}

		// 反向映射
		reverseKey := fmt.Sprintf("qq:reverse:%s", logosID)
		if err := redis.Set(ctx, reverseKey, qqStr, 24*time.Hour); err != nil {
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
	redis := m.getCache()
	if redis == nil {
		logger.Debug("Redis不可用，QQ群映射降级为确定性ID",
			logger.StringField("qq_group", qqStr))
		return logosID, nil
	}

	exists, err := redis.Exists(ctx, key)
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
		if err := redis.HMSet(ctx, key, fields); err != nil {
			logger.Warn("保存QQ群映射失败", logger.ErrorField(err))
		}
		// 设置过期时间
		if err := redis.Expire(ctx, key, 24*time.Hour); err != nil {
			logger.Warn("设置QQ群映射过期时间失败", logger.ErrorField(err))
		}
	}

	return logosID, nil
}

// GetQQNumberByLogosID 根据 Logos ID 获取 QQ 号
func (m *Mapper) GetQQNumberByLogosID(ctx context.Context, logosID string) (string, error) {
	reverseKey := fmt.Sprintf("qq:reverse:%s", logosID)
	redis := m.getCache()
	if redis == nil {
		return "", fmt.Errorf("Redis不可用，无法反查 QQ 号")
	}
	qqNumber, err := redis.Get(ctx, reverseKey)
	if err != nil {
		return "", err
	}
	return qqNumber, nil
}
