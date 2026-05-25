package service

import (
	"Logos/internal/service/messaging/im/dao"
	"Logos/internal/service/messaging/im/model"
	"Logos/internal/service/messaging/types"
	"Logos/pkg/cache"
	"Logos/pkg/logger"
	"Logos/pkg/mq"
	"context"
	"strconv"
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
	StartPresenceConsumer() error
}

type IMServiceImpl struct {
	repo     dao.IMRepository
	cache    cache.Cache
	eventBus *types.EventBus
	ctx      context.Context
}

func NewIMService(repo dao.IMRepository, cache cache.Cache, ctx context.Context) IMService {
	return &IMServiceImpl{
		repo:     repo,
		cache:    cache,
		eventBus: types.GetEventBus(),
		ctx:      ctx,
	}
}

func (s *IMServiceImpl) Connect(ctx context.Context, userID, deviceID, sessionID string) error {
	uid, err := strconv.ParseInt(userID, 10, 64)
	if err != nil {
		return err
	}

	record := &model.OnlineRecord{
		UserID:    uid,
		DeviceID:  deviceID,
		SessionID: sessionID,
		Online:    true,
		LastSeen:  time.Now().UnixMilli(),
		Platform:  "unknown",
	}

	if err := s.repo.UpsertOnlineRecord(ctx, record); err != nil {
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
		userIDStr := strconv.FormatInt(record.UserID, 10)
		records, err := s.repo.GetOnlineRecordsByUser(ctx, userIDStr)
		if err == nil && len(records) == 0 {
			cacheKey := onlineStatusCachePrefix + userIDStr
			if err := s.cache.Delete(ctx, cacheKey); err != nil {
				logger.Warn("清除在线状态缓存失败", logger.ErrorField(err))
			}
		}
		logger.Info("用户离线",
			logger.StringField("user_id", userIDStr),
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
		userIDStr := strconv.FormatInt(record.UserID, 10)
		cacheKey := onlineStatusCachePrefix + userIDStr
		if err := s.cache.Expire(ctx, cacheKey, time.Second*time.Duration(heartbeatTimeout*2)); err != nil {
			logger.Warn("更新缓存过期时间失败", logger.ErrorField(err))
		}
	}

	return nil
}

func (s *IMServiceImpl) SendTypingStatus(ctx context.Context, fromUserID, chatID string, typing bool) error {
	logger.Info("发送输入状态",
		logger.StringField("user_id", fromUserID),
		logger.StringField("chat_id", chatID),
		logger.BoolField("typing", typing))

	typingEvent := &types.TypingEvent{
		UserID:    fromUserID,
		ChatID:    chatID,
		IsTyping:  typing,
		Timestamp: time.Now(),
	}

	if err := s.eventBus.PublishTypingEvent(ctx, typingEvent); err != nil {
		logger.Error("发布输入状态事件失败", logger.ErrorField(err))
		return err
	}

	return nil
}

func (s *IMServiceImpl) BroadcastMessage(ctx context.Context, content string) error {
	logger.Info("广播消息", logger.StringField("content_len", content))

	broadcastEvent := &types.MessageEvent{
		ID:          "broadcast_" + time.Now().Format("20060102150405"),
		ChatID:      "broadcast",
		ChatType:    types.ChatTypeBroadcast,
		SenderID:    "system",
		MessageType: types.MessageTypeText,
		Content:     content,
		Timestamp:   time.Now(),
	}

	if err := s.eventBus.PublishMessageEvent(ctx, broadcastEvent); err != nil {
		logger.Error("发布广播消息事件失败", logger.ErrorField(err))
		return err
	}

	return nil
}

func (s *IMServiceImpl) StartPresenceConsumer() error {
	if s.eventBus == nil {
		logger.Warn("EventBus未初始化，无法启动在线状态消费者")
		return nil
	}

	logger.Info("启动IM服务在线状态消费者")

	go func() {
		handler := func(msg *mq.Message) error {
			return s.handlePresenceEvent(msg)
		}
		if err := s.eventBus.SubscribeIMEvents(s.ctx, handler, "im-service-consumer"); err != nil {
			logger.Error("订阅IM在线状态事件失败", logger.ErrorField(err))
		}
	}()

	return nil
}

func (s *IMServiceImpl) handlePresenceEvent(msg *mq.Message) error {
	event, err := types.UserPresenceEventFromJSON(msg.Value)
	if err != nil {
		logger.Error("解析在线状态事件失败", logger.ErrorField(err))
		return err
	}

	if event.UserID == "" {
		return nil
	}

	ctx := context.Background()

	if event.Online {
		uid, err := strconv.ParseInt(event.UserID, 10, 64)
		if err != nil {
			return err
		}

		record := &model.OnlineRecord{
			UserID:    uid,
			DeviceID:  event.DeviceID,
			SessionID: "ws_" + event.UserID + "_" + event.DeviceID,
			Online:    true,
			LastSeen:  time.Now().UnixMilli(),
			Platform:  "websocket",
		}
		if err := s.repo.UpsertOnlineRecord(ctx, record); err != nil {
			logger.Error("创建在线记录失败", logger.ErrorField(err))
			return err
		}

		cacheKey := onlineStatusCachePrefix + event.UserID
		if err := s.cache.Set(ctx, cacheKey, "1", 0); err != nil {
			logger.Warn("更新在线状态缓存失败", logger.ErrorField(err))
		}

		logger.Info("用户上线（来自Gateway事件）",
			logger.StringField("user_id", event.UserID),
			logger.StringField("device_id", event.DeviceID))
	} else {
		sessionID := "ws_" + event.UserID + "_" + event.DeviceID
		if err := s.repo.SetUserOffline(ctx, sessionID); err != nil {
			logger.Error("设置用户离线失败", logger.ErrorField(err))
			return err
		}

		records, err := s.repo.GetOnlineRecordsByUser(ctx, event.UserID)
		if err == nil && len(records) == 0 {
			cacheKey := onlineStatusCachePrefix + event.UserID
			if err := s.cache.Delete(ctx, cacheKey); err != nil {
				logger.Warn("清除在线状态缓存失败", logger.ErrorField(err))
			}
		}

		logger.Info("用户离线（来自Gateway事件）",
			logger.StringField("user_id", event.UserID),
			logger.StringField("device_id", event.DeviceID))
	}

	return nil
}
