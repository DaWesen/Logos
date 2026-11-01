package offline

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"Logos/internal/service/messaging/types"
	"Logos/pkg/logger"
	"Logos/pkg/mq"
	"Logos/pkg/push"
)

// ErrOfflinePushRetry 表示本轮推送存在「可重试/未知」失败，需要消息队列重投。
// 永久失败（如 410 令牌失效、PayloadTooLarge）不计入该错误：重投也不会成功，
// 只会白白消耗重试次数并更快把消息推进死信。
var ErrOfflinePushRetry = errors.New("offline push: retryable failure")

type OfflinePushService struct {
	pushManager *push.PushManager
	eventBus    *types.EventBus
	ctx         context.Context
	cancel      context.CancelFunc
}

var offlinePush *OfflinePushService

// offlinePushOnce 保证消费者只启动一次。
// 服务初始化可能被多个入口重复调用（测试、热加载、重复注册），
// 若无保护会订阅出两条消费链路：同一通知被推送两遍，且第一条 ctx 永不取消。
var offlinePushOnce sync.Once

func InitOfflinePushService(eventBus *types.EventBus) *OfflinePushService {
	offlinePushOnce.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		offlinePush = &OfflinePushService{
			pushManager: push.GetPushManager(),
			eventBus:    eventBus,
			ctx:         ctx,
			cancel:      cancel,
		}
		offlinePush.startConsumer()
	})
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
			return s.handleNotification(msg)
		})
		if err != nil {
			logger.Error("离线推送: 订阅通知事件失败", logger.ErrorField(err))
		}
	}()
}

// handleNotification 处理一条通知事件，并把失败交给消息队列。
//
// 历史实现无论推送成功与否都返回 nil，等于「推送失败也提交位点」：
// Kafka 的重试与死信机制对这条链路完全失效，一次供应商抖动就永久丢掉推送。
// 现在按失败语义返回错误——可重试/未知交给重投，永久失败（重投也没用）仍返回 nil。
func (s *OfflinePushService) handleNotification(msg *mq.Message) error {
	if !s.pushManager.IsConfigured() {
		return nil
	}

	event, err := types.NotificationEventFromJSON(msg.Value)
	if err != nil {
		// 解析失败属于毒丸：重投必然同样失败，交给上层重试上限后进死信。
		return fmt.Errorf("parse notification event: %w", err)
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

	return summarizePushResults(results)
}

func (s *OfflinePushService) PushMessageNotification(ctx context.Context, event *types.MessageEvent, offlineUserIDs []string) {
	if event == nil {
		return
	}
	// 接收者可能重复（群成员列表拼接、多端会话合并），重复一次就多推一条通知。
	offlineUserIDs = dedupeRecipientIDs(offlineUserIDs)
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

// truncateBody 按「字符」而不是字节截断。
//
// 原实现用 len(s) 判断、s[:maxLen] 切片：中文按 UTF-8 是 3 字节，100 字节只够 33 个汉字；
// 若截断位置落在多字节序列中间，还会产出非法 UTF-8，推送里显示成乱码或整条被丢弃。
func truncateBody(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	return string([]rune(s)[:maxRunes]) + "..."
}

// summarizePushResults 把一批设备推送结果折叠成「本轮是否需要队列重投」。
//
// 空 Kind 表示网络层失败（供应商没给出任何语义），无法判断是否可恢复，按可重试处理；
// 只有供应商明确判定为永久失败（410、PayloadTooLarge、MismatchSenderId）才不重投。
func summarizePushResults(results []push.PushResult) error {
	if len(results) == 0 {
		return nil
	}
	retryable := 0
	for _, r := range results {
		if r.Success {
			continue
		}
		if r.Failure.Kind == push.PushFailurePermanent {
			continue
		}
		retryable++
	}
	if retryable == 0 {
		return nil
	}
	return fmt.Errorf("%w: %d/%d", ErrOfflinePushRetry, retryable, len(results))
}

// dedupeRecipientIDs 去重并剔除空 ID，保持到达顺序。
// 无重复时复用入参切片，避免每次群发都多一次全量拷贝。
func dedupeRecipientIDs(ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	needsWork := false
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			needsWork = true
			continue
		}
		if _, ok := seen[id]; ok {
			needsWork = true
			continue
		}
		seen[id] = struct{}{}
	}
	if !needsWork {
		return ids
	}

	// seen 里带的是「尚未输出」的 ID；输出时删掉，重复项自然被跳过。
	out := make([]string, 0, len(seen))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if _, ok := seen[id]; !ok {
			continue
		}
		delete(seen, id)
		out = append(out, id)
	}
	return out
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
	if event == nil || !s.pushManager.IsConfigured() {
		return
	}

	offlineUserIDs := offlineRecipients(event, onlineUserIDs)
	if len(offlineUserIDs) > 0 {
		s.PushMessageNotification(ctx, event, offlineUserIDs)
	}
}

// offlineRecipients 计算「需要离线推送」的用户集合。
//
// 两个历史问题：
//  1. 群成员列表天然包含发送者，原实现会把群消息推给发送者自己的其它设备——
//     用户刚发完消息，手机却收到「张三发了消息」的通知；
//  2. QQ 桥接是唯一的例外：消息由 QQ 侧注入、再回显给绑定的用户，
//     这里必须保留发送者，否则 QQ 端永远收不到自己发出内容的回执。
//
// 在线名单里出现的用户不推（已有实时下行）。
func offlineRecipients(event *types.MessageEvent, onlineUserIDs []string) []string {
	if event == nil {
		return nil
	}
	recipients := dedupeRecipientIDs(event.RecipientIDs)

	echoToSender := event.Channel == "qq" && event.Metadata != nil && event.Metadata["qq_bound_user"] == "true"

	onlineSet := make(map[string]struct{}, len(onlineUserIDs))
	for _, uid := range onlineUserIDs {
		onlineSet[uid] = struct{}{}
	}

	out := make([]string, 0, len(recipients))
	for _, uid := range recipients {
		if uid == event.SenderID && !echoToSender {
			continue
		}
		if _, online := onlineSet[uid]; online {
			continue
		}
		out = append(out, uid)
	}
	return out
}
