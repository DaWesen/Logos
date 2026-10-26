package mq

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestBuildDeadLetterPayload_KeepsJSONValue(t *testing.T) {
	msg := &Message{
		Topic:     "chat_messages",
		Partition: 2,
		Offset:    88123,
		Key:       "chat-1",
		Value:     []byte(`{"id":"m1"}`),
	}

	payload, err := buildDeadLetterPayload(msg, errors.New("db timeout"))
	if err != nil {
		t.Fatalf("buildDeadLetterPayload error: %v", err)
	}

	var env deadLetterEnvelope
	if err := json.Unmarshal(payload, &env); err != nil {
		t.Fatalf("死信体应为合法 JSON，实际解析失败: %v", err)
	}
	if env.SourceTopic != "chat_messages" || env.Partition != 2 || env.Offset != 88123 || env.Key != "chat-1" {
		t.Fatalf("死信体未保留原始定位: %+v", env)
	}
	if env.Error != "db timeout" {
		t.Fatalf("死信体应记录失败原因，实际 %q", env.Error)
	}
	if string(env.Value) != `{"id":"m1"}` {
		t.Fatalf("合法 JSON 负载应原样保留（便于回放），实际 %s", string(env.Value))
	}
}

func TestBuildDeadLetterPayload_WrapsNonJSONValue(t *testing.T) {
	msg := &Message{Topic: "im_messages", Value: []byte("not-json")}

	payload, err := buildDeadLetterPayload(msg, nil)
	if err != nil {
		t.Fatalf("buildDeadLetterPayload error: %v", err)
	}
	var env deadLetterEnvelope
	if err := json.Unmarshal(payload, &env); err != nil {
		t.Fatalf("死信体必须始终是合法 JSON，实际解析失败: %v", err)
	}
	var raw string
	if err := json.Unmarshal(env.Value, &raw); err != nil {
		t.Fatalf("非法 JSON 负载应包装成字符串，实际 %s", string(env.Value))
	}
	if raw != "not-json" {
		t.Fatalf("包装后的负载内容不符: %q", raw)
	}
	if env.Error != "" {
		t.Fatalf("cause 为 nil 时 error 字段应为空，实际 %q", env.Error)
	}
}

func TestFailureTracker_CountsSameMessageOnly(t *testing.T) {
	var f failureTracker
	msg := &Message{Topic: "chat_messages", Partition: 0, Offset: 10}

	// 首次到达：此前失败次数为 0
	if got := f.record(msg); got != 0 {
		t.Fatalf("首次 record = %d, want 0", got)
	}
	// 同一 offset 重投：依次累计 1,2,3,4，第 5 次（got=4）触发毒丸判定
	for want := 1; want <= 4; want++ {
		if got := f.record(msg); got != want {
			t.Fatalf("第 %d 次重投 record = %d, want %d", want+1, got, want)
		}
	}
	if got := f.record(msg); got+1 < 5 {
		t.Fatalf("第 6 次到达应达到毒丸阈值（failCount+1=%d）", got+1)
	}
}

func TestFailureTracker_DifferentMessageResets(t *testing.T) {
	var f failureTracker
	first := &Message{Topic: "chat_messages", Partition: 0, Offset: 10}
	for i := 0; i < 4; i++ {
		f.record(first)
	}

	// offset 前进（上一条已处理成功）：计数必须归零，正常消息不会被误判为毒丸
	next := &Message{Topic: "chat_messages", Partition: 0, Offset: 11}
	if got := f.record(next); got != 0 {
		t.Fatalf("新消息 record = %d, want 0", got)
	}
	// 同 offset 但不同分区，同样视为不同消息
	if got := f.record(&Message{Topic: "chat_messages", Partition: 1, Offset: 11}); got != 0 {
		t.Fatalf("不同分区 record = %d, want 0", got)
	}
}

func TestFailureTracker_ResetKeepsPosition(t *testing.T) {
	var f failureTracker
	msg := &Message{Topic: "chat_messages", Partition: 0, Offset: 10}
	for i := 0; i < 5; i++ {
		f.record(msg)
	}
	f.reset()

	// 毒丸提交 offset 后若同一条又被拉取到，应重新从第 1 次失败开始计数
	if got := f.record(msg); got != 1 {
		t.Fatalf("reset 后 record = %d, want 1（重新计数）", got)
	}
}
