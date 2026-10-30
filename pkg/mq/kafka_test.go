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
