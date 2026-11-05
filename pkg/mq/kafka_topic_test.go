package mq

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"Logos/config"

	"github.com/segmentio/kafka-go"
)

type fakeTopicAdmin struct {
	create  func(topic string) error
	creates []kafka.TopicConfig
	closes  int
}

func (f *fakeTopicAdmin) CreateTopics(topics ...kafka.TopicConfig) error {
	f.creates = append(f.creates, topics...)
	if f.create == nil {
		return nil
	}
	return f.create(topics[len(topics)-1].Topic)
}

func (f *fakeTopicAdmin) Close() error {
	f.closes++
	return nil
}

func testTopicKafkaConfig() config.Kafka {
	return config.Kafka{
		Brokers:           []string{"broker:9092"},
		Partitions:        3,
		ReplicationFactor: 2,
	}
}

func recordWaits() (func(context.Context, time.Duration) bool, *[]time.Duration) {
	waits := &[]time.Duration{}
	return func(_ context.Context, d time.Duration) bool {
		*waits = append(*waits, d)
		return true
	}, waits
}

func TestCreateTopicWithRetry_SucceedsOnRetry(t *testing.T) {
	attempts := 0
	admin := &fakeTopicAdmin{create: func(string) error {
		attempts++
		if attempts == 1 {
			return errors.New("temporary failure")
		}
		return nil
	}}
	wait, waits := recordWaits()
	dials := 0
	dial := func(string) (topicAdmin, error) {
		dials++
		return admin, nil
	}

	err := createTopicWithRetry(context.Background(), dial, wait, "broker:9092", testTopicKafkaConfig(), "data_collection")
	if err != nil {
		t.Fatalf("第二次尝试成功时不应返回错误: %v", err)
	}
	if dials != 2 {
		t.Fatalf("应重连一次，got %d", dials)
	}
	if len(*waits) != 1 || (*waits)[0] != 2*time.Second {
		t.Fatalf("失败一次后应只等待 2s，got %v", *waits)
	}
	if len(admin.creates) != 2 {
		t.Fatalf("应调用两次 CreateTopics，got %d", len(admin.creates))
	}
	if admin.closes != 2 {
		t.Fatalf("每次尝试都应关闭连接，got %d", admin.closes)
	}
	if admin.creates[0].NumPartitions != 3 || admin.creates[0].ReplicationFactor != 2 {
		t.Fatalf("主题配置未从 config 读取: %+v", admin.creates[0])
	}
}

func TestCreateTopicWithRetry_NoWaitAfterFinalAttempt(t *testing.T) {
	admin := &fakeTopicAdmin{create: func(string) error {
		return errors.New("invalid replication factor")
	}}
	wait, waits := recordWaits()
	dials := 0
	dial := func(string) (topicAdmin, error) {
		dials++
		return admin, nil
	}

	err := createTopicWithRetry(context.Background(), dial, wait, "broker:9092", testTopicKafkaConfig(), "chat_messages")
	if err == nil {
		t.Fatal("三次尝试都失败时应返回错误")
	}
	if dials != topicCreateMaxAttempts {
		t.Fatalf("应尝试 %d 次，got %d", topicCreateMaxAttempts, dials)
	}
	// 2s + 4s；最后一次失败后不应再等 6s。
	if len(*waits) != 2 || (*waits)[0] != 2*time.Second || (*waits)[1] != 4*time.Second {
		t.Fatalf("最后一次失败后不应继续等待，got %v", *waits)
	}
	if !strings.Contains(err.Error(), "chat_messages") {
		t.Fatalf("错误应带 topic 便于定位，got %v", err)
	}
}

func TestCreateTopicWithRetry_CanceledContextStopsRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	admin := &fakeTopicAdmin{create: func(string) error {
		return errors.New("still failing")
	}}
	dials := 0
	dial := func(string) (topicAdmin, error) {
		dials++
		return admin, nil
	}
	wait := func(context.Context, time.Duration) bool { return false }

	err := createTopicWithRetry(ctx, dial, wait, "broker:9092", testTopicKafkaConfig(), "user_events")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("取消应保留 context.Canceled，got %v", err)
	}
	if dials != 1 {
		t.Fatalf("取消后不应继续尝试，got %d 次", dials)
	}
}

func TestCreateTopic_ZeroBrokersDoesNotPanic(t *testing.T) {
	err := (&KafkaManager{}).CreateTopic("user_events")
	if err == nil || !strings.Contains(err.Error(), "brokers") {
		t.Fatalf("空 brokers 应返回明确错误而不是 panic，got %v", err)
	}
}

func TestCreateTopics_AbortsWhenBrokerUnreachable(t *testing.T) {
	dialErr := errors.New("connection refused")
	dials := 0
	dial := func(string) (topicAdmin, error) {
		dials++
		return nil, dialErr
	}
	wait, waits := recordWaits()

	err := createTopics(context.Background(), testTopicKafkaConfig(), dial, wait)
	if err == nil {
		t.Fatal("broker 不可达时应返回错误")
	}
	if !errors.Is(err, errKafkaBrokerUnreachable) || !errors.Is(err, dialErr) {
		t.Fatalf("错误应同时保留哨兵与原始原因，got %v", err)
	}
	// 旧实现会对 9 个 topic 各重试 3 次（27 次 Dial、108s 等待）。
	if dials != topicCreateMaxAttempts {
		t.Fatalf("broker 不可达应立即止损，got %d 次 Dial", dials)
	}
	if len(*waits) != 2 {
		t.Fatalf("只应等待 2s+4s，got %v", *waits)
	}
}

func TestCreateTopics_ContinuesAfterTopicRejected(t *testing.T) {
	admin := &fakeTopicAdmin{create: func(topic string) error {
		if topic == "user_events" {
			return errors.New("invalid replication factor")
		}
		return nil
	}}
	dial := func(string) (topicAdmin, error) { return admin, nil }
	wait, waits := recordWaits()

	err := createTopics(context.Background(), testTopicKafkaConfig(), dial, wait)
	if err == nil {
		t.Fatal("某个 topic 失败时整体应返回错误")
	}
	if !strings.Contains(err.Error(), "user_events") {
		t.Fatalf("聚合错误应指明失败的 topic，got %v", err)
	}
	if len(admin.creates) != 9+2 {
		// 9 个 topic 各一次，失败的那个额外重试 2 次。
		t.Fatalf("其余 topic 不应被跳过，got %d 次 CreateTopics", len(admin.creates))
	}
	seen := make(map[string]bool, len(admin.creates))
	for _, tc := range admin.creates {
		seen[tc.Topic] = true
	}
	if !seen["bot_events"] {
		t.Fatal("最后一个 topic 也应被尝试")
	}
	if len(*waits) != 2 {
		t.Fatalf("只有失败的 topic 需要等待，got %v", *waits)
	}
}

func TestCreateTopics_CanceledBeforeStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dials := 0
	dial := func(string) (topicAdmin, error) {
		dials++
		return &fakeTopicAdmin{}, nil
	}

	err := createTopics(ctx, testTopicKafkaConfig(), dial, func(context.Context, time.Duration) bool { return true })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("已取消的 ctx 应直接返回 context.Canceled，got %v", err)
	}
	if dials != 0 {
		t.Fatalf("ctx 已取消时不应再拨号，got %d", dials)
	}
}
