package ctxmem

import (
	"context"
	"sync"
)

// SummaryStore 摘要状态的持久化接口。
//
// 拆成接口是为了让「进程内 map」与「Redis」共享同一套上层逻辑：
// 单飞、CAS、熔断、降级都不应该知道底层存哪。
type SummaryStore interface {
	// Load 返回会话当前摘要状态；不存在时 ok=false。
	Load(ctx context.Context, conversationID string) (state SummaryState, ok bool, err error)
	// Save 只在 state.Order 晚于已存状态时写入，返回是否写入成功。
	Save(ctx context.Context, state SummaryState) (bool, error)
}

// MemorySummaryStore 进程内实现，仅用于测试与 Redis 不可用时的兜底。
//
// 它不能跨副本、重启即丢，所以生产环境必须以 Redis 为主；
// 但在单机部署或 Redis 抖动期间，它至少保证同一进程内的摘要不会退化为空。
type MemorySummaryStore struct {
	mu     sync.RWMutex
	states map[string]SummaryState
}

func NewMemorySummaryStore() *MemorySummaryStore {
	return &MemorySummaryStore{states: make(map[string]SummaryState)}
}

func (s *MemorySummaryStore) Load(_ context.Context, conversationID string) (SummaryState, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	state, ok := s.states[conversationID]
	return state, ok, nil
}

func (s *MemorySummaryStore) Save(_ context.Context, state SummaryState) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if current, ok := s.states[state.ConversationID]; ok && !state.Order.After(current.Order) {
		return false, nil
	}
	s.states[state.ConversationID] = state
	return true, nil
}

// FallbackSummaryStore 先写主存储，失败时退回兜底存储。
//
// 读路径先读主存储，未命中或报错再读兜底；写路径总是尽量双写，
// 这样 Redis 短暂不可用时进程内仍有最新摘要，恢复后下一次写入会
// 把状态带回 Redis。
type FallbackSummaryStore struct {
	Primary  SummaryStore
	Fallback SummaryStore
}

func (s FallbackSummaryStore) Load(ctx context.Context, conversationID string) (SummaryState, bool, error) {
	if s.Primary != nil {
		state, ok, err := s.Primary.Load(ctx, conversationID)
		if err == nil && ok {
			return state, true, nil
		}
	}
	if s.Fallback != nil {
		return s.Fallback.Load(ctx, conversationID)
	}
	return SummaryState{}, false, nil
}

func (s FallbackSummaryStore) Save(ctx context.Context, state SummaryState) (bool, error) {
	saved := false
	var primaryErr error
	if s.Primary != nil {
		ok, err := s.Primary.Save(ctx, state)
		if err != nil {
			primaryErr = err
		} else {
			saved = ok
		}
	}
	fallbackSaved := false
	var fallbackErr error
	if s.Fallback != nil {
		ok, err := s.Fallback.Save(ctx, state)
		if err == nil {
			fallbackSaved = ok
		} else {
			fallbackErr = err
		}
	}
	saved = saved || fallbackSaved
	// 只要任一存储写成功，本轮摘要就是可用的：上层不该因为 Redis
	// 抖动而触发熔断，否则一次缓存故障会让整个进程停止刷新摘要。
	// 只有两边都失败时才向上报错；优先返回主存储的错误，便于定位根因。
	if saved {
		return true, nil
	}
	if primaryErr != nil {
		return false, primaryErr
	}
	if fallbackErr != nil {
		return false, fallbackErr
	}
	return false, nil
}
