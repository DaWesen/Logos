package gateway

import (
	"testing"
	"time"
)

// =====================================================================
// 测试 1：UnifiedConnectionManager.LocalSendToUser 本地投递正确
// =====================================================================
func TestUnifiedManager_LocalSendToUser(t *testing.T) {
	m := &UnifiedConnectionManager{
		connections:  make(map[string]*UnifiedConnection),
		userSessions: make(map[string]map[string]bool),
	}

	received := make(chan []byte, 1)
	m.Register("sess1", "user1", "dev1", "ws", func(data []byte) {
		received <- data
	})

	m.LocalSendToUser("user1", []byte("hello"))

	select {
	case got := <-received:
		if string(got) != "hello" {
			t.Errorf("expected 'hello', got %s", got)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timeout waiting for local send")
	}
}

// =====================================================================
// 测试 2：UnifiedConnectionManager.SendToUser broadcaster=nil 退化为纯本地
//        验证向后兼容（单节点部署不启用广播时行为正确）
// =====================================================================
func TestUnifiedManager_SendToUser_LocalOnly_WhenBroadcasterNil(t *testing.T) {
	m := &UnifiedConnectionManager{
		connections:  make(map[string]*UnifiedConnection),
		userSessions: make(map[string]map[string]bool),
	}
	// 不注入 broadcaster，保持为 nil
	if m.broadcaster != nil {
		t.Fatal("expected nil broadcaster by default")
	}

	received := make(chan []byte, 1)
	m.Register("sess1", "user1", "dev1", "ws", func(data []byte) {
		received <- data
	})

	// SendToUser 内部 publishToRemote 应在 broadcaster=nil 时 no-op
	// 不应 panic，应只做本地投递
	m.SendToUser("user1", []byte("local-only"))

	select {
	case got := <-received:
		if string(got) != "local-only" {
			t.Errorf("expected 'local-only', got %s", got)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timeout waiting for local-only send")
	}
}

// =====================================================================
// 测试 3：UnifiedConnectionManager.Register / Unregister 维护连接表
// =====================================================================
func TestUnifiedManager_RegisterUnregister(t *testing.T) {
	m := &UnifiedConnectionManager{
		connections:  make(map[string]*UnifiedConnection),
		userSessions: make(map[string]map[string]bool),
	}

	var called int
	m.Register("sess1", "user1", "dev1", "ws", func(data []byte) { called++ })

	// 验证 userSessions 维护正确
	m.mu.RLock()
	sessions, ok := m.userSessions["user1"]
	m.mu.RUnlock()
	if !ok || len(sessions) != 1 {
		t.Errorf("expected 1 session for user1, got %v", m.userSessions)
	}
	_ = called // 仅注册，不实际调用

	m.Unregister("sess1")
	m.mu.RLock()
	_, stillExists := m.connections["sess1"]
	m.mu.RUnlock()
	if stillExists {
		t.Error("connection should be removed after Unregister")
	}
}

// =====================================================================
// 测试 4：BroadcastMessage 结构体字段语义
//        确保 SourceNodeID 在跨节点广播中保留，供回环过滤使用
// =====================================================================
func TestBroadcastMessage_Fields(t *testing.T) {
	msg := &BroadcastMessage{
		SourceNodeID:    "node-1",
		TargetMode:      TargetModeUsers,
		TargetUserIDs:   []string{"u1", "u2"},
		ExceptUserID:    "u3",
		Payload:         []byte("payload"),
	}

	if msg.SourceNodeID != "node-1" {
		t.Errorf("expected node-1, got %s", msg.SourceNodeID)
	}
	if msg.TargetMode != TargetModeUsers {
		t.Errorf("expected %s, got %s", TargetModeUsers, msg.TargetMode)
	}
	if len(msg.TargetUserIDs) != 2 {
		t.Errorf("expected 2 user ids, got %d", len(msg.TargetUserIDs))
	}
}

// =====================================================================
// 测试 5：UnifiedConnectionManager.HandleRemoteMessage 各 TargetMode 路由
//        验证订阅侧的分发逻辑（不依赖 redis）
//        这是 Broadcaster.Subscribe handler 的纯本地等价测试
// =====================================================================
func TestUnifiedManager_HandleRemoteMessage_Routing(t *testing.T) {
	m := &UnifiedConnectionManager{
		connections:  make(map[string]*UnifiedConnection),
		userSessions: make(map[string]map[string]bool),
	}

	// 注册三个用户
	userAReceived := make(chan []byte, 4)
	userBReceived := make(chan []byte, 4)
	userCReceived := make(chan []byte, 4)

	m.Register("sess-A", "userA", "devA", "ws", func(d []byte) { userAReceived <- d })
	m.Register("sess-B", "userB", "devB", "ws", func(d []byte) { userBReceived <- d })
	m.Register("sess-C", "userC", "devC", "ws", func(d []byte) { userCReceived <- d })

	// 测试 TargetMode=user：远端消息指定 userA
	m.HandleRemoteMessage(&BroadcastMessage{
		TargetMode:   TargetModeUser,
		TargetUserID: "userA",
		Payload:      []byte("to-A"),
	})
	select {
	case got := <-userAReceived:
		if string(got) != "to-A" {
			t.Errorf("expected 'to-A', got %s", got)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("userA did not receive 'to-A'")
	}

	// 测试 TargetMode=users：远端消息指定 [userB, userC]
	m.HandleRemoteMessage(&BroadcastMessage{
		TargetMode:    TargetModeUsers,
		TargetUserIDs: []string{"userB", "userC"},
		Payload:       []byte("to-BC"),
	})
	for _, ch := range []chan []byte{userBReceived, userCReceived} {
		select {
		case got := <-ch:
			if string(got) != "to-BC" {
				t.Errorf("expected 'to-BC', got %s", got)
			}
		case <-time.After(100 * time.Millisecond):
			t.Error("user did not receive 'to-BC'")
		}
	}

	// 测试 TargetMode=broadcast：广播给所有连接
	m.HandleRemoteMessage(&BroadcastMessage{
		TargetMode: TargetModeBroadcast,
		Payload:    []byte("all"),
	})
	for _, ch := range []chan []byte{userAReceived, userBReceived, userCReceived} {
		select {
		case got := <-ch:
			if string(got) != "all" {
				t.Errorf("expected 'all', got %s", got)
			}
		case <-time.After(100 * time.Millisecond):
			t.Error("user did not receive 'all' broadcast")
		}
	}

	// 测试 TargetMode=broadcast with ExceptUserID
	m.HandleRemoteMessage(&BroadcastMessage{
		TargetMode:   TargetModeBroadcast,
		ExceptUserID: "userA",
		Payload:      []byte("all-except-A"),
	})
	// userA 不应收到
	select {
	case got := <-userAReceived:
		t.Errorf("userA should NOT receive 'all-except-A', but got %s", got)
	case <-time.After(50 * time.Millisecond):
		// 预期
	}
	// userB、userC 应收到
	for _, ch := range []chan []byte{userBReceived, userCReceived} {
		select {
		case got := <-ch:
			if string(got) != "all-except-A" {
				t.Errorf("expected 'all-except-A', got %s", got)
			}
		case <-time.After(100 * time.Millisecond):
			t.Error("user did not receive 'all-except-A'")
		}
	}
}

// =====================================================================
// 测试 6：LocalBroadcastMessageExcept 排除指定用户
// =====================================================================
func TestUnifiedManager_LocalBroadcastMessageExcept(t *testing.T) {
	m := &UnifiedConnectionManager{
		connections:  make(map[string]*UnifiedConnection),
		userSessions: make(map[string]map[string]bool),
	}

	userAReceived := make(chan []byte, 4)
	userBReceived := make(chan []byte, 4)

	m.Register("sess-A", "userA", "devA", "ws", func(d []byte) { userAReceived <- d })
	m.Register("sess-B", "userB", "devB", "ws", func(d []byte) { userBReceived <- d })

	m.LocalBroadcastMessageExcept([]byte("except-A"), "userA")

	// userA 不应收到
	select {
	case <-userAReceived:
		t.Error("userA should NOT receive message")
	case <-time.After(50 * time.Millisecond):
		// 预期
	}
	// userB 应收到
	select {
	case got := <-userBReceived:
		if string(got) != "except-A" {
			t.Errorf("expected 'except-A', got %s", got)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("userB did not receive 'except-A'")
	}
}

// =====================================================================
// 测试 7：SendToUsers 本地多用户投递
// =====================================================================
func TestUnifiedManager_SendToUsers_Local(t *testing.T) {
	m := &UnifiedConnectionManager{
		connections:  make(map[string]*UnifiedConnection),
		userSessions: make(map[string]map[string]bool),
	}

	userAReceived := make(chan []byte, 4)
	userBReceived := make(chan []byte, 4)

	m.Register("sess-A", "userA", "devA", "ws", func(d []byte) { userAReceived <- d })
	m.Register("sess-B", "userB", "devB", "ws", func(d []byte) { userBReceived <- d })

	// broadcaster=nil 时只走本地
	m.SendToUsers([]string{"userA", "userB"}, []byte("multi"))

	for _, ch := range []chan []byte{userAReceived, userBReceived} {
		select {
		case got := <-ch:
			if string(got) != "multi" {
				t.Errorf("expected 'multi', got %s", got)
			}
		case <-time.After(100 * time.Millisecond):
			t.Error("user did not receive 'multi'")
		}
	}
}

// =====================================================================
// 测试 8：SendToUser 投递给不存在用户时不 panic
// =====================================================================
func TestUnifiedManager_SendToUser_NonExistentUser(t *testing.T) {
	m := &UnifiedConnectionManager{
		connections:  make(map[string]*UnifiedConnection),
		userSessions: make(map[string]map[string]bool),
	}
	// 不应 panic
	m.LocalSendToUser("non-existent", []byte("noop"))
	m.SendToUser("non-existent", []byte("noop-with-publish"))
	m.LocalBroadcastMessage([]byte("noop-broadcast"))
	m.LocalBroadcastMessageExcept([]byte("noop-except"), "non-existent")
}
