package gateway

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"
)

// batchRecordingStore 记录每次 HDel 的批次大小，用于断言 presence 剔除按批进行。
// 可选 failAfter：第 N 次 HDel 起返回错误，验证「失败即停止」不会继续冲击 Redis。
type batchRecordingStore struct {
	data      map[string]map[string]string
	batches   [][]string
	calls     int
	failAfter int
	err       error
}

func newBatchRecordingStore() *batchRecordingStore {
	return &batchRecordingStore{
		data: make(map[string]map[string]string),
		err:  errors.New("redis unavailable"),
	}
}

func (s *batchRecordingStore) HSet(_ context.Context, key, field string, value interface{}) error {
	if s.data[key] == nil {
		s.data[key] = make(map[string]string)
	}
	s.data[key][field] = fmt.Sprint(value)
	return nil
}

func (s *batchRecordingStore) HGetAll(_ context.Context, key string) (map[string]string, error) {
	out := make(map[string]string, len(s.data[key]))
	for k, v := range s.data[key] {
		out[k] = v
	}
	return out, nil
}

func (s *batchRecordingStore) HDel(_ context.Context, key string, fields []string) error {
	s.calls++
	s.batches = append(s.batches, append([]string{}, fields...))
	if s.failAfter > 0 && s.calls >= s.failAfter {
		return s.err
	}
	for _, field := range fields {
		delete(s.data[key], field)
	}
	if len(s.data[key]) == 0 {
		delete(s.data, key)
	}
	return nil
}

// TestClassifyPresence 穷举分类规则：新鲜/边界/超时/脏数据。
func TestClassifyPresence(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	cutoff := now.Add(-presenceStaleAfter).UnixMilli()

	fields := map[string]string{
		"fresh":    strconv.FormatInt(now.UnixMilli(), 10),
		"boundary": strconv.FormatInt(cutoff, 10),            // 恰好在截止时刻：仍活跃
		"stale":    strconv.FormatInt(cutoff-1, 10),          // 超过窗口 1ms：失效
		"invalid":  "not-a-number",                           // 脏数据：失效
		"legacy":   "0",                                      // 时间戳 0：失效
		"just-now": strconv.FormatInt(now.UnixMilli()+1, 10), // 轻微未来时间：活跃
	}

	active, stale := classifyPresence(fields, now)
	if active != 3 {
		t.Fatalf("active = %d, want 3（fresh/boundary/just-now）", active)
	}
	if len(stale) != 3 {
		t.Fatalf("stale = %v, want 3 个", stale)
	}
	want := []string{"invalid", "legacy", "stale"}
	for i := range want {
		if stale[i] != want[i] {
			t.Fatalf("stale 应按 sessionID 排序 = %v, want %v", stale, want)
		}
	}
}

func TestClassifyPresence_Empty(t *testing.T) {
	active, stale := classifyPresence(nil, time.Now())
	if active != 0 || len(stale) != 0 {
		t.Fatalf("空快照 = (%d, %v), want (0, nil)", active, stale)
	}
}

// TestPruneBatchesBySize 验证大量失效会话被按 PresencePruneBatchSize 分批删除，
// 且活跃会话不受影响。一次 HDel 参数包过大是 Redis 单线程上的隐性故障源。
func TestPruneBatchesBySize(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	store := newBatchRecordingStore()
	key := presenceKey("7001")

	const staleCount = 650
	old := strconv.FormatInt(now.Add(-presenceStaleAfter-time.Minute).UnixMilli(), 10)
	store.data[key] = make(map[string]string, staleCount+1)
	for i := 0; i < staleCount; i++ {
		store.data[key][fmt.Sprintf("stale-%04d", i)] = old
	}
	store.data[key]["fresh"] = strconv.FormatInt(now.UnixMilli(), 10)

	s := NewRedisPresenceStore(store)
	active, added, err := s.Touch(ctx, "7001", "new-session", now)
	if err != nil || !added {
		t.Fatalf("Touch = (%d, %v, %v), want (_, true, nil)", active, added, err)
	}
	// 活跃会话 = fresh + new-session
	if active != 2 {
		t.Fatalf("active = %d, want 2", active)
	}

	if len(store.batches) != 4 {
		t.Fatalf("650 条失效会话应分 4 批（200/200/200/50），实际 %d 批: %v", len(store.batches), store.batches)
	}
	for i, batch := range store.batches {
		if len(batch) > PresencePruneBatchSize {
			t.Fatalf("第 %d 批大小 %d 超过上限 %d", i, len(batch), PresencePruneBatchSize)
		}
		if i < len(store.batches)-1 && len(batch) != PresencePruneBatchSize {
			t.Fatalf("第 %d 批应满批 %d，实际 %d", i, PresencePruneBatchSize, len(batch))
		}
	}
	if _, ok := store.data[key]["fresh"]; !ok {
		t.Fatal("活跃会话不应被剔除")
	}
	if _, ok := store.data[key]["stale-0000"]; ok {
		t.Fatal("失效会话应被剔除")
	}
}

// TestPruneStopsOnStoreError Redis 出错时立即停止后续批次，避免故障期间放大压力；
// 已删除的批次不回滚，剩余失效会话留到下一次操作继续处理。
func TestPruneStopsOnStoreError(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	store := newBatchRecordingStore()
	store.failAfter = 1
	key := presenceKey("7002")

	old := strconv.FormatInt(now.Add(-presenceStaleAfter-time.Minute).UnixMilli(), 10)
	store.data[key] = make(map[string]string, 500)
	for i := 0; i < 500; i++ {
		store.data[key][fmt.Sprintf("stale-%04d", i)] = old
	}

	s := NewRedisPresenceStore(store)
	if _, _, err := s.Touch(ctx, "7002", "new-session", now); err != nil {
		t.Fatalf("Touch 不应因剔除失败而报错（剔除是尽力而为）: %v", err)
	}
	if len(store.batches) != 1 {
		t.Fatalf("首次 HDel 失败后应立即停止，实际尝试 %d 批", len(store.batches))
	}
	// 500 条失效 + 本次 Touch 新登记的 new-session，且首批 HDel 失败未删任何字段
	if len(store.data[key]) != 501 {
		t.Fatalf("失败的批次不应删除任何字段，剩余 %d（want 501）", len(store.data[key]))
	}
}
