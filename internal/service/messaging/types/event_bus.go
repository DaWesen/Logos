package types

import (
	"context"
	"fmt"
	"sync"

	"Logos/pkg/logger"
	"Logos/pkg/mq"
)

type EventBus struct {
	producer  *mq.Producer
	consumers map[string]*mq.Consumer
	mu        sync.RWMutex
}

var (
	eventBusInstance *EventBus
	eventBusOnce     sync.Once
)

func GetEventBus() *EventBus {
	eventBusOnce.Do(func() {
		eventBusInstance = &EventBus{
			producer:  mq.NewProducer(),
			consumers: make(map[string]*mq.Consumer),
		}
	})
	return eventBusInstance
}

func (eb *EventBus) PublishMessageEvent(ctx context.Context, event *MessageEvent) error {
	if eb.producer == nil {
		return fmt.Errorf("producer not initialized")
	}

	data, err := event.ToJSON()
	if err != nil {
		return fmt.Errorf("serialize event failed: %w", err)
	}

	key := event.ChatID
	logger.Info("发布消息事件",
		logger.StringField("chat_id", key),
		logger.StringField("event_id", event.ID))

	return eb.producer.SendChatEvent(ctx, key, data)
}

func (eb *EventBus) PublishPresenceEvent(ctx context.Context, event *UserPresenceEvent) error {
	if eb.producer == nil {
		return fmt.Errorf("producer not initialized")
	}

	data, err := event.ToJSON()
	if err != nil {
		return fmt.Errorf("serialize event failed: %w", err)
	}

	key := event.UserID
	logger.Info("发布用户在线事件",
		logger.StringField("user_id", key),
		logger.BoolField("online", event.Online))

	return eb.producer.SendIMEvent(ctx, key, data)
}

func (eb *EventBus) PublishNotification(ctx context.Context, event *NotificationEvent) error {
	if eb.producer == nil {
		return fmt.Errorf("producer not initialized")
	}

	data, err := event.ToJSON()
	if err != nil {
		return fmt.Errorf("serialize event failed: %w", err)
	}

	key := event.UserID
	logger.Info("发布通知事件",
		logger.StringField("user_id", key),
		logger.StringField("type", event.Type))

	return eb.producer.SendNotification(ctx, key, data)
}

func (eb *EventBus) PublishMessageReadEvent(ctx context.Context, event *MessageReadEvent) error {
	if eb.producer == nil {
		return fmt.Errorf("producer not initialized")
	}

	data, err := event.ToJSON()
	if err != nil {
		return fmt.Errorf("serialize event failed: %w", err)
	}

	key := event.ChatID
	logger.Info("发布消息已读事件",
		logger.StringField("chat_id", key),
		logger.StringField("reader_id", event.ReaderID),
		logger.IntField("message_count", len(event.MessageIDs)))

	return eb.producer.SendChatEvent(ctx, key, data)
}

func (eb *EventBus) PublishTypingEvent(ctx context.Context, event *TypingEvent) error {
	if eb.producer == nil {
		return fmt.Errorf("producer not initialized")
	}

	data, err := event.ToJSON()
	if err != nil {
		return fmt.Errorf("serialize event failed: %w", err)
	}

	key := event.ChatID
	logger.Info("发布输入状态事件",
		logger.StringField("chat_id", key),
		logger.StringField("user_id", event.UserID),
		logger.BoolField("typing", event.IsTyping))

	return eb.producer.SendChatEvent(ctx, key, data)
}

func (eb *EventBus) SubscribeIMEvents(ctx context.Context, handler mq.MessageHandler, groupID ...string) error {
	eb.mu.Lock()
	defer eb.mu.Unlock()

	consumerGroupID := "gateway-im-consumer"
	if len(groupID) > 0 && groupID[0] != "" {
		consumerGroupID = groupID[0]
	}

	consumerKey := "im-" + consumerGroupID
	if _, exists := eb.consumers[consumerKey]; exists {
		logger.Warn("IM消费者已存在", logger.StringField("group_id", consumerGroupID))
		return nil
	}

	consumer := mq.NewConsumer(mq.TopicIM, consumerGroupID)
	eb.consumers[consumerKey] = consumer

	logger.Info("开始订阅IM事件", logger.StringField("group_id", consumerGroupID))
	return consumer.Subscribe(ctx, handler)
}

func (eb *EventBus) SubscribeChatEvents(ctx context.Context, handler mq.MessageHandler, groupID ...string) error {
	eb.mu.Lock()
	defer eb.mu.Unlock()

	consumerGroupID := "gateway-chat-consumer"
	if len(groupID) > 0 && groupID[0] != "" {
		consumerGroupID = groupID[0]
	}

	if _, exists := eb.consumers[consumerGroupID]; exists {
		logger.Warn("Chat消费者已存在", logger.StringField("group_id", consumerGroupID))
		return nil
	}

	consumer := mq.NewConsumer(mq.TopicChat, consumerGroupID)
	eb.consumers[consumerGroupID] = consumer

	logger.Info("开始订阅Chat事件", logger.StringField("group_id", consumerGroupID))
	return consumer.Subscribe(ctx, handler)
}

func (eb *EventBus) SubscribeNotifications(ctx context.Context, handler mq.MessageHandler) error {
	eb.mu.Lock()
	defer eb.mu.Unlock()

	if _, exists := eb.consumers["notification"]; exists {
		logger.Warn("Notification消费者已存在")
		return nil
	}

	consumer := mq.NewConsumer(mq.TopicNotification, "gateway-notification-consumer")
	eb.consumers["notification"] = consumer

	logger.Info("开始订阅通知事件")
	return consumer.Subscribe(ctx, handler)
}

func (eb *EventBus) Close() error {
	eb.mu.Lock()
	defer eb.mu.Unlock()

	if eb.producer != nil {
		_ = eb.producer.Close()
	}

	for name, consumer := range eb.consumers {
		_ = consumer.Close()
		delete(eb.consumers, name)
		logger.Info("关闭消费者", logger.StringField("name", name))
	}

	return nil
}
