package service

import (
	"context"
	"strings"
	"testing"

	"Logos/pkg/mq"
)

// TestStartKafkaConsumer_SubscribeErrorSurfaces 验证订阅失败会同步返回给调用方。
// 修复前每个 topic 的 Subscribe 都跑在裸 goroutine 里，初始化失败只留一行日志，
// cmd 层拿到的永远是 nil，qa_request / recommendation / system_event 三条链路
// 可以在没有任何消费者的状态下“启动成功”。
func TestStartKafkaConsumer_SubscribeErrorSurfaces(t *testing.T) {
	original := newMessageConsumer
	newMessageConsumer = func(string, string) *mq.Consumer { return &mq.Consumer{} }
	t.Cleanup(func() { newMessageConsumer = original })

	svc := &messageServiceImpl{}

	err := svc.StartKafkaConsumer(context.Background())
	if err == nil {
		t.Fatal("订阅失败时应返回启动错误")
	}
	if !strings.Contains(err.Error(), mq.TopicQARequest) {
		t.Fatalf("错误应包含失败的 topic，实际: %v", err)
	}
	if !strings.Contains(err.Error(), "consumer not initialized") {
		t.Fatalf("错误应保留底层原因，实际: %v", err)
	}
}
