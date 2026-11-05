package offline

import (
	"context"
	"errors"
	"strings"
	"testing"

	"Logos/internal/service/messaging/types"
	"Logos/pkg/mq"
)

func TestNewOfflinePushService_RejectsNilEventBus(t *testing.T) {
	service, err := newOfflinePushService(nil)
	if err == nil || !strings.Contains(err.Error(), "event bus not initialized") {
		t.Fatalf("期望 event bus 未初始化错误，实际 service=%v err=%v", service, err)
	}
	if service != nil {
		t.Fatalf("初始化失败时不应返回半成品实例，实际 %v", service)
	}
}

func TestStartConsumer_PropagatesSubscribeError(t *testing.T) {
	original := subscribeNotificationEvents
	t.Cleanup(func() { subscribeNotificationEvents = original })

	var gotGroupID string
	subscribeNotificationEvents = func(_ *types.EventBus, _ context.Context, _ mq.MessageHandler, groupID string) error {
		gotGroupID = groupID
		return errors.New("kafka unavailable")
	}

	service := &OfflinePushService{eventBus: &types.EventBus{}}
	err := service.startConsumer()
	if err == nil {
		t.Fatal("订阅失败必须返回错误，不能只记日志后当作启动成功")
	}
	if !strings.Contains(err.Error(), "subscribe notifications") || !strings.Contains(err.Error(), "kafka unavailable") {
		t.Fatalf("错误应包含订阅动作与底层原因，实际 %v", err)
	}
	if gotGroupID != offlinePushConsumerGroup {
		t.Fatalf("离线推送必须使用独立消费组，实际 group=%q", gotGroupID)
	}
}

func TestStartConsumer_SuccessReturnsNil(t *testing.T) {
	original := subscribeNotificationEvents
	t.Cleanup(func() { subscribeNotificationEvents = original })

	subscribeNotificationEvents = func(_ *types.EventBus, _ context.Context, _ mq.MessageHandler, _ string) error {
		return nil
	}

	service := &OfflinePushService{eventBus: &types.EventBus{}}
	if err := service.startConsumer(); err != nil {
		t.Fatalf("订阅成功不应返回错误，实际 %v", err)
	}
}
