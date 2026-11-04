package gateway

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateTarget(t *testing.T) {
	payload := []byte(`{"k":"v"}`)

	cases := []struct {
		name string
		msg  *BroadcastMessage
		want string // 空串表示期望通过
	}{
		{"nil", nil, "消息为空"},
		{"空 payload", &BroadcastMessage{TargetMode: TargetModeUser, TargetUserID: "u"}, "payload 为空"},
		{"user 缺 user_id", &BroadcastMessage{TargetMode: TargetModeUser, Payload: payload}, "缺少 target_user_id"},
		{"session 缺 session_id", &BroadcastMessage{TargetMode: TargetModeSession, TargetUserID: "u", Payload: payload}, "缺少 target_user_id 或 target_session_id"},
		{"users 空列表", &BroadcastMessage{TargetMode: TargetModeUsers, Payload: payload}, "target_user_ids 为空"},
		{"未知模式", &BroadcastMessage{TargetMode: "uzen", Payload: payload}, "未知 target_mode"},
		{"user 合法", &BroadcastMessage{TargetMode: TargetModeUser, TargetUserID: "u", Payload: payload}, ""},
		{"session 合法", &BroadcastMessage{TargetMode: TargetModeSession, TargetUserID: "u", TargetSessionID: "s", Payload: payload}, ""},
		{"users 合法", &BroadcastMessage{TargetMode: TargetModeUsers, TargetUserIDs: []string{"u"}, Payload: payload}, ""},
		{"broadcast 合法", &BroadcastMessage{TargetMode: TargetModeBroadcast, Payload: payload}, ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateTarget(c.msg)
			if c.want == "" {
				if err != nil {
					t.Fatalf("期望通过，got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want 包含 %q, got %v", c.want, err)
			}
		})
	}
}

// TestDecodeBroadcastPayload_SizeLimit 上限必须在 Unmarshal 之前生效，
// 否则一条超大消息已经完整进内存了。
func TestDecodeBroadcastPayload_SizeLimit(t *testing.T) {
	huge := []byte(`{"payload":"` + strings.Repeat("a", broadcastPayloadLimit) + `"}`)

	if _, err := decodeBroadcastPayload(huge); !errors.Is(err, errBroadcastPayloadTooLarge) {
		t.Fatalf("want errBroadcastPayloadTooLarge, got %v", err)
	}

	if _, err := decodeBroadcastPayload([]byte(`{`)); err == nil {
		t.Fatal("非法 JSON 应返回 error")
	}

	bm, err := decodeBroadcastPayload([]byte(`{"source_node_id":"n1","target_mode":"user","payload":"aGk="}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bm.SourceNodeID != "n1" || bm.TargetMode != TargetModeUser {
		t.Fatalf("字段解析错误: %+v", bm)
	}
}

// TestSafeHandle_RecoversPanic handler panic 不能穿透到订阅 goroutine，
// 否则一条异常消息会直接终止整个网关进程。
func TestSafeHandle_RecoversPanic(t *testing.T) {
	b := &Broadcaster{channel: "ch"}

	panicked := false
	handler := func(*BroadcastMessage) { panicked = true; panic("boom") }

	// 不应抛出 panic
	b.safeHandle(handler, &BroadcastMessage{TargetMode: TargetModeUser})

	// 恢复后仍可处理后续消息
	ok := false
	b.safeHandle(func(*BroadcastMessage) { ok = true }, &BroadcastMessage{TargetMode: TargetModeUser})
	if !panicked || !ok {
		t.Fatal("panic 后订阅应继续工作")
	}
}
