package tcp

import (
	"encoding/json"
	"testing"
)

// TestIncomingMessageUsesDataKey 上行帧载荷字段必须与 WS 一致（data），避免同一协议两套字段名。
func TestIncomingMessageUsesDataKey(t *testing.T) {
	raw := []byte(`{"type":"typing","request_id":"r1","data":{"chat_id":"private_1001_1002","typing":true}}`)

	var msg IncomingMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if msg.Type != MessageTypeTyping {
		t.Fatalf("type = %v, want typing", msg.Type)
	}
	if msg.RequestID != "r1" {
		t.Fatalf("request_id = %q, want r1", msg.RequestID)
	}

	var payload TypingPayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.ChatID != "private_1001_1002" || !payload.Typing {
		t.Fatalf("unexpected payload: %+v", payload)
	}
}

// TestOutgoingEnvelopeCarriesData 下行帧必须用 data 字段，不能残留旧的 payload 字段名。
func TestOutgoingEnvelopeCarriesData(t *testing.T) {
	env := OutgoingMessage{Type: MessageTypeAck, RequestID: "r1", Payload: AckPayload{MessageID: "m1"}}
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out map[string]interface{}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := out["data"]; !ok {
		t.Fatalf("outgoing frame missing data key: %s", raw)
	}
	if _, ok := out["payload"]; ok {
		t.Fatalf("outgoing frame must not use legacy payload key: %s", raw)
	}
}
