package gateway

import (
	"sync"
	"testing"
)

// TestDedupeUserIDs 覆盖保序、剔除空 ID、不修改入参三种语义。
func TestDedupeUserIDs(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{"nil", nil, nil},
		{"单元素", []string{"a"}, []string{"a"}},
		{"单个空ID", []string{""}, nil},
		{"保序去重", []string{"a", "b", "a", "", "b", "c"}, []string{"a", "b", "c"}},
		{"全部重复", []string{"a", "a", "a"}, []string{"a"}},
		{"全部为空", []string{"", ""}, nil},
		{"无重复", []string{"a", "b", "c"}, []string{"a", "b", "c"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			original := append([]string(nil), tc.in...)
			got := dedupeUserIDs(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("长度不符: got=%v want=%v", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("第 %d 项不符: got=%v want=%v", i, got, tc.want)
				}
			}
			if len(original) == len(tc.in) {
				for i := range original {
					if original[i] != tc.in[i] {
						t.Fatalf("入参被修改: before=%v after=%v", original, tc.in)
					}
				}
			}
		})
	}
}

// TestDedupeUserIDs_NoDuplicateReusesInput 验证无重复时不额外拷贝切片。
func TestDedupeUserIDs_NoDuplicateReusesInput(t *testing.T) {
	in := []string{"a", "b", "c"}
	got := dedupeUserIDs(in)
	if &got[0] != &in[0] {
		t.Fatal("无重复时应直接返回入参切片")
	}
}

func newTestManager() *UnifiedConnectionManager {
	return &UnifiedConnectionManager{
		connections:  make(map[string]*UnifiedConnection),
		userSessions: make(map[string]map[string]bool),
	}
}

// TestSendToUsers_DeliversOncePerUser 验证重复 userID 在本地投递前已被去重：
// 每个会话只收到一帧，而不是按重复次数收到多帧。
func TestSendToUsers_DeliversOncePerUser(t *testing.T) {
	m := newTestManager()

	var mu sync.Mutex
	deliveries := make(map[string]int)
	record := func(sessionID string) SendFunc {
		return func([]byte) {
			mu.Lock()
			deliveries[sessionID]++
			mu.Unlock()
		}
	}
	m.Register("s1", "u1", "d1", "websocket", record("s1"))
	m.Register("s2", "u1", "d2", "websocket", record("s2"))
	m.Register("s3", "u2", "d3", "tcp", record("s3"))

	m.SendToUsers([]string{"u1", "u1", "u2", "u1", ""}, []byte("payload"))

	mu.Lock()
	defer mu.Unlock()
	if deliveries["s1"] != 1 || deliveries["s2"] != 1 || deliveries["s3"] != 1 {
		t.Fatalf("每个会话应只投递一次，实际 %v", deliveries)
	}
}

// TestSendToUsers_AllEmptySkips 验证全空名单不会触发任何投递。
func TestSendToUsers_AllEmptySkips(t *testing.T) {
	m := newTestManager()

	called := false
	m.Register("s1", "u1", "d1", "websocket", func([]byte) { called = true })

	m.SendToUsers([]string{"", ""}, []byte("payload"))

	if called {
		t.Fatal("空名单不应投递")
	}
}

// TestHandleRemoteMessage_DedupesUsers 验证远端广播消息里的重复目标只投递一次。
func TestHandleRemoteMessage_DedupesUsers(t *testing.T) {
	m := newTestManager()

	var mu sync.Mutex
	count := 0
	m.Register("s1", "u1", "d1", "websocket", func([]byte) {
		mu.Lock()
		count++
		mu.Unlock()
	})

	m.HandleRemoteMessage(&BroadcastMessage{
		TargetMode:    TargetModeUsers,
		TargetUserIDs: []string{"u1", "u1", "", "u1"},
		Payload:       []byte("payload"),
	})

	mu.Lock()
	defer mu.Unlock()
	if count != 1 {
		t.Fatalf("远端重复目标应只投递一次，实际 %d", count)
	}
}
