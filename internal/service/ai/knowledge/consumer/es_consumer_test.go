package consumer

import (
	"encoding/json"
	"errors"
	"testing"

	"Logos/pkg/mq"
)

func TestHandleMessage_RejectsMalformedEventContract(t *testing.T) {
	cases := []struct {
		name  string
		event map[string]interface{}
	}{
		{
			name:  "missing action",
			event: map[string]interface{}{"entity": map[string]interface{}{}},
		},
		{
			name:  "action is not a string",
			event: map[string]interface{}{"action": float64(1)},
		},
		{
			name:  "action is blank",
			event: map[string]interface{}{"action": "   "},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &ESConsumer{}
			payload, _ := json.Marshal(tc.event)
			err := c.handleMessage(&mq.Message{Value: payload})
			if !errors.Is(err, mq.ErrSkipMessage) {
				t.Fatalf("契约错误应标记为不可恢复消息，实际 %v", err)
			}
		})
	}
}

func TestHandleMessage_UnknownActionIsVisible(t *testing.T) {
	c := &ESConsumer{}
	payload, _ := json.Marshal(map[string]interface{}{"action": "upsert"})

	err := c.handleMessage(&mq.Message{Value: payload})
	if !errors.Is(err, mq.ErrSkipMessage) {
		t.Fatalf("未知 action 不应静默提交 offset，实际 %v", err)
	}
}
