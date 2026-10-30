package gateway

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"Logos/internal/service/messaging/types"
	"Logos/pkg/logger"

	"go.uber.org/zap"
)

// TestMain 用 nop logger，避免 logger 初始化去读仓库根目录的 config 文件。
func TestMain(m *testing.M) {
	logger.SetLogger(zap.NewNop())
	os.Exit(m.Run())
}

// fakeHashStore 内存版哈希存储，用于测试 RedisPresenceStore 的剔除语义。
type fakeHashStore struct {
	data map[string]map[string]string
}

func newFakeHashStore() *fakeHashStore {
	return &fakeHashStore{data: make(map[string]map[string]string)}
}

func (f *fakeHashStore) HSet(_ context.Context, key, field string, value interface{}) error {
	if f.data[key] == nil {
		f.data[key] = make(map[string]string)
	}
	f.data[key][field] = fmt.Sprint(value)
	return nil
}

func (f *fakeHashStore) HGetAll(_ context.Context, key string) (map[string]string, error) {
	out := make(map[string]string, len(f.data[key]))
	for k, v := range f.data[key] {
		out[k] = v
	}
	return out, nil
}

func (f *fakeHashStore) HDel(_ context.Context, key string, fields []string) error {
	for _, field := range fields {
		delete(f.data[key], field)
	}
	if len(f.data[key]) == 0 {
		delete(f.data, key)
	}
	return nil
}

type recordingPublisher struct {
	events []types.UserPresenceEvent
}

func (p *recordingPublisher) PublishPresenceEvent(_ context.Context, event *types.UserPresenceEvent) error {
	p.events = append(p.events, *event)
	return nil
}

// failingStore 模拟 Redis 不可用。
type failingStore struct{ err error }

func (s failingStore) Touch(context.Context, string, string, time.Time) (int64, bool, error) {
	return 0, false, s.err
}

func (s failingStore) Remove(context.Context, string, string, time.Time) (int64, bool, error) {
	return 0, false, s.err
}

// TestPresenceTrackerPublishesOnlyOnTransition 多设备登录只发一次 online，
// 关掉其中一台设备不发任何事件，最后一台设备离线才发 offline。
func TestPresenceTrackerPublishesOnlyOnTransition(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	store := NewRedisPresenceStore(newFakeHashStore())
	pub := &recordingPublisher{}
	tr := NewPresenceTracker(store, pub)
	tr.now = func() time.Time { return now }

	// 桌面端上线 → online
	tr.Online(ctx, "1001", "desktop", "s1")
	if len(pub.events) != 1 || !pub.events[0].Online {
		t.Fatalf("first connection should publish online, got %+v", pub.events)
	}

	// 手机端上线 → 仍是同一用户在线，不应重复广播
	now = now.Add(5 * time.Second)
	tr.Online(ctx, "1001", "mobile", "s2")
	if len(pub.events) != 1 {
		t.Fatalf("second connection must not publish, got %+v", pub.events)
	}

	// 关掉手机端 → 桌面端还在线，不应广播离线
	now = now.Add(5 * time.Second)
	tr.Offline(ctx, "1001", "mobile", "s2")
	if len(pub.events) != 1 {
		t.Fatalf("one device disconnect must not publish offline, got %+v", pub.events)
	}

	// 关掉桌面端 → 最后一个会话，广播离线
	now = now.Add(5 * time.Second)
	tr.Offline(ctx, "1001", "desktop", "s1")
	if len(pub.events) != 2 || pub.events[1].Online {
		t.Fatalf("last disconnect should publish offline, got %+v", pub.events)
	}

	// 不同用户的在线状态互不影响
	now = now.Add(5 * time.Second)
	tr.Online(ctx, "1002", "desktop", "s3")
	if len(pub.events) != 3 || !pub.events[2].Online {
		t.Fatalf("another user's first connection should publish online, got %+v", pub.events)
	}
}

// TestPresenceTrackerIdempotentOnDuplicateSessions 重复/乱序（重复注销、重复上线）不产生多余事件。
func TestPresenceTrackerIdempotentOnDuplicateSessions(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	store := NewRedisPresenceStore(newFakeHashStore())
	pub := &recordingPublisher{}
	tr := NewPresenceTracker(store, pub)
	tr.now = func() time.Time { return now }

	tr.Online(ctx, "2001", "desktop", "s1")
	tr.Online(ctx, "2001", "desktop", "s1") // 重复登记同一会话
	if len(pub.events) != 1 {
		t.Fatalf("duplicate online must not publish extra events, got %+v", pub.events)
	}

	tr.Offline(ctx, "2001", "desktop", "s1")
	tr.Offline(ctx, "2001", "desktop", "s1") // 重复注销：会话已不存在，不应再发事件
	if len(pub.events) != 2 || pub.events[1].Online {
		t.Fatalf("duplicate offline must be idempotent, got %+v", pub.events)
	}
	if len(pub.events) != 2 {
		t.Fatalf("duplicate offline must not publish extra events, got %+v", pub.events)
	}
}

// TestPresenceTrackerTouchKeepsSessionAlive 心跳只续期，不产生事件。
func TestPresenceTrackerTouchKeepsSessionAlive(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	store := NewRedisPresenceStore(newFakeHashStore())
	pub := &recordingPublisher{}
	tr := NewPresenceTracker(store, pub)
	tr.now = func() time.Time { return now }

	tr.Online(ctx, "3001", "desktop", "s1")
	now = now.Add(presenceStaleAfter)
	tr.Touch(ctx, "3001", "s1")

	if len(pub.events) != 1 {
		t.Fatalf("heartbeat must not publish events, got %+v", pub.events)
	}

	// 续期后离线仍判定为用户离线（会话没有因为超时被剔除而漏发 offline）
	now = now.Add(10 * time.Second)
	tr.Offline(ctx, "3001", "desktop", "s1")
	if len(pub.events) != 2 || pub.events[1].Online {
		t.Fatalf("touch should keep session alive until offline, got %+v", pub.events)
	}
}

// TestPresenceTrackerFailOpenOnStoreError 存储故障时按单连接语义照常发布，不丢在线状态。
func TestPresenceTrackerFailOpenOnStoreError(t *testing.T) {
	ctx := context.Background()
	pub := &recordingPublisher{}
	tr := NewPresenceTracker(failingStore{err: errors.New("redis down")}, pub)

	tr.Online(ctx, "4001", "desktop", "s1")
	tr.Offline(ctx, "4001", "desktop", "s1")

	if len(pub.events) != 2 || !pub.events[0].Online || pub.events[1].Online {
		t.Fatalf("store failure should fail open, got %+v", pub.events)
	}
}

// TestRedisPresenceStorePrunesStaleSessions 节点宕机遗留的会话超时后被剔除，
// 不会把用户永久固定在「在线」。
func TestRedisPresenceStorePrunesStaleSessions(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	store := NewRedisPresenceStore(newFakeHashStore())

	active, added, err := store.Touch(ctx, "5001", "s1", now)
	if err != nil || active != 1 || !added {
		t.Fatalf("first touch = (%d, %v, %v), want (1, true, nil)", active, added, err)
	}

	now = now.Add(30 * time.Second)
	active, added, err = store.Touch(ctx, "5001", "s2", now)
	if err != nil || active != 2 || !added {
		t.Fatalf("second touch = (%d, %v, %v), want (2, true, nil)", active, added, err)
	}

	// s1/s2 都超过存活窗口未续期，再操作时应被剔除，只剩新登记的 s3
	now = now.Add(presenceStaleAfter + time.Second)
	active, added, err = store.Touch(ctx, "5001", "s3", now)
	if err != nil || active != 1 || !added {
		t.Fatalf("touch after stale = (%d, %v, %v), want (1, true, nil)", active, added, err)
	}

	// 只剩 s3：注销后返回 0 才应发布离线
	active, removed, err := store.Remove(ctx, "5001", "s3", now)
	if err != nil || active != 0 || !removed {
		t.Fatalf("remove after stale = (%d, %v, %v), want (0, true, nil)", active, removed, err)
	}
}

// TestRedisPresenceStorePrunesInvalidTimestamp 时间戳脏数据按失效处理，避免用户永远在线。
func TestRedisPresenceStorePrunesInvalidTimestamp(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	hash := newFakeHashStore()
	hash.data[presenceKey("6001")] = map[string]string{
		"s1":       "not-a-number",
		"s-legacy": "0",
	}
	store := NewRedisPresenceStore(hash)
	active, added, err := store.Touch(ctx, "6001", "s2", now)
	if err != nil || active != 1 || !added {
		t.Fatalf("touch = (%d, %v, %v), want (1, true, nil)", active, added, err)
	}
	if _, ok := hash.data[presenceKey("6001")]["s1"]; ok {
		t.Fatalf("invalid timestamp session should be pruned")
	}
}
