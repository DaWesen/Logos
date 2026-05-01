package service

import (
	"Logos/internal/service/messaging/im/dao"
	"Logos/internal/service/messaging/im/model"
	"Logos/pkg/cache"
	"Logos/pkg/logger"
	"context"
	"errors"
	"time"
)

const (
	onlineStatusCachePrefix = "im:online:"
	heartbeatTimeout        = 60
)

type IMService interface {
	Connect(ctx context.Context, userID, deviceID, sessionID string) error
	Disconnect(ctx context.Context, sessionID string) error
	GetOnlineStatus(ctx context.Context, userIDs []string) (map[string]bool, error)
	SetOnlineStatus(ctx context.Context, userID string, online bool) error
	Heartbeat(ctx context.Context, sessionID string) error
	SendTypingStatus(ctx context.Context, fromUserID, chatID string, typing bool) error
	BroadcastMessage(ctx context.Context, content string) error
	GetOnlineUsers(ctx context.Context) ([]string, error)
	ValidateSession(ctx context.Context, sessionID string) (string, error)
}

type IMServiceImpl struct {
	repo  dao.IMRepository
	cache cache.Cache
	ctx   context.Context
}

func NewIMService(repo dao.IMRepository, cache cache.Cache, ctx context.Context) IMService {
	return &IMServiceImpl{
		repo:  repo,
		cache: cache,
		ctx:   ctx,
	}
}

func (s *IMServiceImpl) Connect(ctx context.Context, userID, deviceID, sessionID string) error {
	record := &model.OnlineRecord{
		UserID:    userID,
		DeviceID:  deviceID,
		SessionID: sessionID,
		Online:    true,
		LastSeen:  time.Now().UnixMilli(),
		Platform:  "unknown",
	}

	if err := s.repo.CreateOnlineRecord(ctx, record); err != nil {
		logger.Error("创建在线记录失败", logger.ErrorField(err))
		return err
	}

	cacheKey := onlineStatusCachePrefix + userID
	if err := s.cache.Set(ctx, cacheKey, "1", 0); err != nil {
		logger.Warn("更新在线状态缓存失败", logger.ErrorField(err))
	}

	logger.Info("用户上线",
		logger.StringField("user_id", userID),
		logger.StringField("device_id", deviceID),
		logger.StringField("session_id", sessionID))

	return nil
}

func (s *IMServiceImpl) Disconnect(ctx context.Context, sessionID string) error {
	record, err := s.repo.GetOnlineRecordBySession(ctx, sessionID)
	if err != nil {
		logger.Warn("查询会话记录失败", logger.StringField("session_id", sessionID))
	}

	if err := s.repo.SetUserOffline(ctx, sessionID); err != nil {
		logger.Error("设置用户离线失败", logger.ErrorField(err))
		return err
	}

	if record != nil {
		records, err := s.repo.GetOnlineRecordsByUser(ctx, record.UserID)
		if err == nil && len(records) == 0 {
			cacheKey := onlineStatusCachePrefix + record.UserID
			if err := s.cache.Delete(ctx, cacheKey); err != nil {
				logger.Warn("清除在线状态缓存失败", logger.ErrorField(err))
			}
		}
		logger.Info("用户离线",
			logger.StringField("user_id", record.UserID),
			logger.StringField("session_id", sessionID))
	}

	return nil
}

func (s *IMServiceImpl) GetOnlineStatus(ctx context.Context, userIDs []string) (map[string]bool, error) {
	if len(userIDs) == 0 {
		return make(map[string]bool), nil
	}

	status := make(map[string]bool)
	uncachedIDs := make([]string, 0, len(userIDs))

	for _, userID := range userIDs {
		cacheKey := onlineStatusCachePrefix + userID
		exists, err := s.cache.Exists(ctx, cacheKey)
		if err == nil && exists {
			status[userID] = true
		} else {
			uncachedIDs = append(uncachedIDs, userID)
		}
	}

	if len(uncachedIDs) > 0 {
		dbStatus, err := s.repo.GetBatchOnlineStatus(ctx, uncachedIDs)
		if err != nil {
			logger.Error("批量查询在线状态失败", logger.ErrorField(err))
			return nil, err
		}
		for userID, online := range dbStatus {
			status[userID] = online
			if online {
				cacheKey := onlineStatusCachePrefix + userID
				if err := s.cache.Set(ctx, cacheKey, "1", 0); err != nil {
					logger.Warn("更新在线状态缓存失败", logger.ErrorField(err))
				}
			}
		}
	}

	return status, nil
}

func (s *IMServiceImpl) SetOnlineStatus(ctx context.Context, userID string, online bool) error {
	cacheKey := onlineStatusCachePrefix + userID
	if online {
		if err := s.cache.Set(ctx, cacheKey, "1", 0); err != nil {
			logger.Warn("设置在线状态缓存失败", logger.ErrorField(err))
		}
	} else {
		if err := s.repo.SetAllUserOffline(ctx, userID); err != nil {
			logger.Error("设置用户所有会话离线失败", logger.ErrorField(err))
			return err
		}
		if err := s.cache.Delete(ctx, cacheKey); err != nil {
			logger.Warn("清除在线状态缓存失败", logger.ErrorField(err))
		}
	}
	logger.Info("设置用户在线状态",
		logger.StringField("user_id", userID),
		logger.BoolField("online", online))
	return nil
}

func (s *IMServiceImpl) Heartbeat(ctx context.Context, sessionID string) error {
	if err := s.repo.UpdateLastSeen(ctx, sessionID); err != nil {
		logger.Warn("更新心跳时间失败",
			logger.StringField("session_id", sessionID),
			logger.ErrorField(err))
		return err
	}

	record, err := s.repo.GetOnlineRecordBySession(ctx, sessionID)
	if err == nil && record != nil {
		cacheKey := onlineStatusCachePrefix + record.UserID
		if err := s.cache.Expire(ctx, cacheKey, time.Second*time.Duration(heartbeatTimeout*2)); err != nil {
			logger.Warn("更新缓存过期时间失败", logger.ErrorField(err))
		}
	}

	return nil
}

func (s *IMServiceImpl) SendTypingStatus(ctx context.Context, fromUserID, chatID string, typing bool) error {
	logger.Info("输入状态",
		logger.StringField("user_id", fromUserID),
		logger.StringField("chat_id", chatID),
		logger.BoolField("typing", typing))
	return nil
}

func (s *IMServiceImpl) BroadcastMessage(ctx context.Context, content string) error {
	logger.Info("广播消息", logger.StringField("content", content))
	return nil
}

func (s *IMServiceImpl) GetOnlineUsers(ctx context.Context) ([]string, error) {
	return s.repo.GetOnlineUsers(ctx)
}

func (s *IMServiceImpl) ValidateSession(ctx context.Context, sessionID string) (string, error) {
	record, err := s.repo.GetOnlineRecordBySession(ctx, sessionID)
	if err != nil {
		return "", errors.New("会话不存在")
	}
	if !record.Online {
		return "", errors.New("会话已失效")
	}
	return record.UserID, nil
}
