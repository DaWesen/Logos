package service

import (
	"encoding/json"
	"errors"
	"testing"

	"Logos/pkg/mq"
)

func userActivityMessage(t *testing.T, payload any) *mq.Message {
	t.Helper()

	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("构造事件载荷失败: %v", err)
	}
	return &mq.Message{Topic: mq.TopicUserActivity, Key: "k-1", Value: raw}
}

func TestHandleUserActivityEventAcceptsCompleteContract(t *testing.T) {
	svc := &collectionServiceImpl{}
	msg := userActivityMessage(t, map[string]interface{}{
		"user_id":     "user-1",
		"action":      "view",
		"target_type": "dataset",
	})

	if err := svc.handleUserActivityEvent(msg); err != nil {
		t.Fatalf("合法事件不应报错，实际: %v", err)
	}
}

func TestHandleUserActivityEventAllowsEmptyOptionalTargetType(t *testing.T) {
	svc := &collectionServiceImpl{}

	for name, payload := range map[string]map[string]interface{}{
		"缺少 target_type": {"user_id": "user-1", "action": "view"},
		"target_type 为 null": {
			"user_id": "user-1", "action": "view", "target_type": nil,
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := svc.handleUserActivityEvent(userActivityMessage(t, payload)); err != nil {
				t.Fatalf("可选字段缺失不应报错，实际: %v", err)
			}
		})
	}
}

func TestHandleUserActivityEventRejectsContractViolations(t *testing.T) {
	svc := &collectionServiceImpl{}

	cases := map[string]map[string]interface{}{
		"缺少 user_id":       {"action": "view"},
		"user_id 为 null":   {"user_id": nil, "action": "view"},
		"user_id 为数字":      {"user_id": 42, "action": "view"},
		"user_id 为空白":      {"user_id": "   ", "action": "view"},
		"缺少 action":        {"user_id": "user-1"},
		"action 为对象":       {"user_id": "user-1", "action": map[string]interface{}{"name": "view"}},
		"action 为空串":       {"user_id": "user-1", "action": ""},
		"target_type 类型错误": {"user_id": "user-1", "action": "view", "target_type": 1},
		"生产端信封缺少全部契约字段":    {},
	}

	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			err := svc.handleUserActivityEvent(userActivityMessage(t, payload))
			if !errors.Is(err, mq.ErrSkipMessage) {
				t.Fatalf("期望 ErrSkipMessage（契约不符应跳过而非重试），实际: %v", err)
			}
		})
	}
}

func TestHandleUserActivityEventSkipsNullPayload(t *testing.T) {
	svc := &collectionServiceImpl{}
	msg := &mq.Message{Topic: mq.TopicUserActivity, Value: []byte("null")}

	err := svc.handleUserActivityEvent(msg)
	if !errors.Is(err, mq.ErrSkipMessage) {
		t.Fatalf("null 载荷应作为契约不符跳过，实际: %v", err)
	}
}

func TestHandleUserActivityEventKeepsMalformedJSONForDeadLetter(t *testing.T) {
	svc := &collectionServiceImpl{}
	msg := &mq.Message{Topic: mq.TopicUserActivity, Value: []byte("{not-json")}

	err := svc.handleUserActivityEvent(msg)
	if err == nil {
		t.Fatal("非法 JSON 必须报错，不能静默提交 offset")
	}
	if errors.Is(err, mq.ErrSkipMessage) {
		t.Fatalf("非法 JSON 应走重试/死信以保留原始载荷，实际: %v", err)
	}
}

func TestParseUserActivityEventTrimsWhitespace(t *testing.T) {
	activity, err := parseUserActivityEvent(map[string]interface{}{
		"user_id": "  user-1  ",
		"action":  " view ",
	})
	if err != nil {
		t.Fatalf("解析合法事件失败: %v", err)
	}
	if activity.UserID != "user-1" || activity.Action != "view" {
		t.Fatalf("未按预期裁剪空白: %+v", activity)
	}
}
