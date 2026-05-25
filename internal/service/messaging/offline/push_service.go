package offline

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"Logos/internal/service/messaging/types"
	"Logos/pkg/logger"
	"Logos/pkg/mq"
	"Logos/pkg/push"
)

type OfflinePushService struct {
	pushManager *push.PushManager
	eventBus    *types.EventBus
	ctx         context.Context
	cancel      context.CancelFunc
}

var offlinePush *OfflinePushService

func InitOfflinePushService(eventBus *types.EventBus) *OfflinePushService {
	ctx, cancel := context.WithCancel(context.Background())
	offlinePush = &OfflinePushService{
		pushManager: push.GetPushManager(),
		eventBus:    eventBus,
		ctx:         ctx,
		cancel:      cancel,
	}
	offlinePush.startConsumer()
	return offlinePush
}

func GetOfflinePushService() *OfflinePushService {
	return offlinePush
}

func (s *OfflinePushService) startConsumer() {
	if s.eventBus == nil {
		return
	}

	go func() {
		err := s.eventBus.SubscribeNotifications(s.ctx, func(msg *mq.Message) error {
			s.handleNotification(msg)
			return nil
		})
		if err != nil {
			logger.Error("离线推送: 订阅通知事件失败", logger.ErrorField(err))
		}
	}()
}

func (s *OfflinePushService) handleNotification(msg *mq.Message) {
	if !s.pushManager.IsConfigured() {
		return
	}

	event, err := types.NotificationEventFromJSON(msg.Value)
	if err != nil {
		logger.Warn("离线推送: 解析通知事件失败", logger.ErrorField(err))
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	data := make(map[string]string)
	for k, v := range event.Metadata {
		data[k] = v
	}
	data["notification_id"] = event.ID
	data["notification_type"] = event.Type

	results := s.pushManager.SendToUser(ctx, event.UserID, event.Title, event.Content, data)

	successCount := 0
	for _, r := range results {
		if r.Success {
			successCount++
		}
	}

	if successCount > 0 {
		logger.Info("离线推送已发送",
			logger.StringField("user_id", event.UserID),
			logger.IntField("success", successCount),
			logger.IntField("total", len(results)))
	}
}

func (s *OfflinePushService) PushMessageNotification(ctx context.Context, event *types.MessageEvent, offlineUserIDs []string) {
	if !s.pushManager.IsConfigured() || len(offlineUserIDs) == 0 {
		return
	}

	senderName := event.Metadata["sender_name"]
	if senderName == "" {
		senderName = "用户"
	}

	title := senderName
	chatType := event.ChatType
	switch chatType {
	case types.ChatTypeGroup:
		groupName := event.Metadata["group_name"]
		if groupName == "" {
			groupName = "群聊"
		}
		title = fmt.Sprintf("%s(%s)", senderName, groupName)
	case types.ChatTypeBroadcast:
		title = "广播消息"
	}

	body := truncateBody(event.Content, 100)
	if event.MessageType != types.MessageTypeText {
		body = mediaTypeDescription(event.MessageType)
	}

	data := map[string]string{
		"chat_id":      event.ChatID,
		"message_id":   event.ID,
		"chat_type":    strconv.Itoa(int(event.ChatType)),
		"message_type": strconv.Itoa(int(event.MessageType)),
		"sender_id":    event.SenderID,
	}

	s.pushManager.SendToUsers(ctx, offlineUserIDs, title, body, data)
}

func (s *OfflinePushService) PushFriendRequestNotification(ctx context.Context, userID, fromUserName, message string) {
	if !s.pushManager.IsConfigured() {
		return
	}

	data := map[string]string{
		"notification_type": "friend_request",
	}

	s.pushManager.SendToUser(ctx, userID, "好友请求", fmt.Sprintf("%s 请求添加你为好友", fromUserName), data)
}

func (s *OfflinePushService) PushGroupInvitationNotification(ctx context.Context, userID, inviterName, groupName string) {
	if !s.pushManager.IsConfigured() {
		return
	}

	data := map[string]string{
		"notification_type": "group_invitation",
	}

	s.pushManager.SendToUser(ctx, userID, "群组邀请", fmt.Sprintf("%s 邀请你加入群组 %s", inviterName, groupName), data)
}

func (s *OfflinePushService) RegisterPushToken(token *push.PushToken) {
	s.pushManager.RegisterToken(token)
}

func (s *OfflinePushService) UnregisterPushToken(userID, deviceID string) {
	s.pushManager.UnregisterToken(userID, deviceID)
}

func (s *OfflinePushService) Close() {
	s.cancel()
}

func truncateBody(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func mediaTypeDescription(msgType types.MessageType) string {
	switch msgType {
	case types.MessageTypeImage:
		return "[图片]"
	case types.MessageTypeFile:
		return "[文件]"
	case types.MessageTypeVoice:
		return "[语音]"
	case types.MessageTypeVideo:
		return "[视频]"
	case types.MessageTypeLocation:
		return "[位置]"
	default:
		return "[消息]"
	}
}

func (s *OfflinePushService) PushFromMessageEvent(ctx context.Context, event *types.MessageEvent, onlineUserIDs []string) {
	if !s.pushManager.IsConfigured() || len(event.RecipientIDs) == 0 {
		return
	}

	onlineSet := make(map[string]struct{}, len(onlineUserIDs))
	for _, uid := range onlineUserIDs {
		onlineSet[uid] = struct{}{}
	}

	var offlineUserIDs []string
	for _, uid := range event.RecipientIDs {
		if _, online := onlineSet[uid]; !online {
			offlineUserIDs = append(offlineUserIDs, uid)
		}
	}

	if len(offlineUserIDs) > 0 {
		s.PushMessageNotification(ctx, event, offlineUserIDs)
	}
}

func init() {
	_ = json.Marshal
	_ = strings.Contains
}
