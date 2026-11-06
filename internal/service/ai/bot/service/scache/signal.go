package scache

import (
	"sync"
	"time"
)

// hitMark 是一次「刚命中过」的快照。
//
// 只存归一化 query 与向量，不存答案：信号层的职责是发现问题，
// 不是再保存一份用户数据。向量与 query 都来自内存，TTL 到期即丢弃。
type hitMark struct {
	normQuery string
	vec       []float32
	at        time.Time
}

// signals 负责两个假命中信号：
//
//  1. 重问：命中后短时间内又问了一个高度相似的问题。如果答案真的解决了
//     问题，用户不应该在两分钟内换一种说法再问一遍。
//  2. 否定反馈：下一条消息是「不对 / 不是这个 / 重新回答」。
//
// 两者都只是「疑似」，不能直接证明假命中，所以只打点、不改变命中结果；
// 但它让一个原本黑盒的缓存第一次变得可观测（§13.3）。
type signals struct {
	mu       sync.Mutex
	items    map[string][]hitMark
	maxScope int
	maxTotal int
	ttl      time.Duration
}

func newSignals(maxTotal int, ttl time.Duration) *signals {
	if maxTotal <= 0 {
		maxTotal = 1024
	}
	if ttl <= 0 {
		ttl = 2 * time.Minute
	}
	return &signals{
		items:    make(map[string][]hitMark),
		maxScope: 8,
		maxTotal: maxTotal,
		ttl:      ttl,
	}
}

// remember 记录一次命中；同一 scope 只保留最近 maxScope 条。
func (s *signals) remember(scopeHash, normQuery string, vec []float32, now time.Time) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	s.pruneLocked(now)
	if _, ok := s.items[scopeHash]; !ok && len(s.items) >= s.maxTotal {
		s.dropOldestScopeLocked()
	}
	marks := s.items[scopeHash]
	marks = append(marks, hitMark{normQuery: normQuery, vec: vec, at: now})
	if len(marks) > s.maxScope {
		marks = marks[len(marks)-s.maxScope:]
	}
	s.items[scopeHash] = marks
}

// reasked 判断当前问题是否像是对最近一次命中的「换个说法再问」。
func (s *signals) reasked(scopeHash string, vec []float32, threshold float64, now time.Time) bool {
	if s == nil || len(vec) == 0 {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, mark := range s.items[scopeHash] {
		if now.Sub(mark.at) > s.ttl || len(mark.vec) == 0 {
			continue
		}
		if CosineSimilarity(vec, mark.vec) >= threshold {
			return true
		}
	}
	return false
}

// rejected 判断当前输入是否是明确的否定反馈。
// 只在 TTL 内存在命中记录时才算，避免把用户正常的「不对，我想问的是……」误报。
func (s *signals) rejected(scopeHash, normalized string, now time.Time) bool {
	if s == nil || !isRejectFeedback(normalized) {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, mark := range s.items[scopeHash] {
		if now.Sub(mark.at) <= s.ttl {
			return true
		}
	}
	return false
}

func (s *signals) pruneLocked(now time.Time) {
	for scope, marks := range s.items {
		kept := marks[:0]
		for _, mark := range marks {
			if now.Sub(mark.at) <= s.ttl {
				kept = append(kept, mark)
			}
		}
		if len(kept) == 0 {
			delete(s.items, scope)
			continue
		}
		s.items[scope] = kept
	}
}

func (s *signals) dropOldestScopeLocked() {
	var oldestScope string
	var oldest time.Time
	for scope, marks := range s.items {
		for _, mark := range marks {
			if oldestScope == "" || mark.at.Before(oldest) {
				oldestScope = scope
				oldest = mark.at
			}
		}
	}
	if oldestScope != "" {
		delete(s.items, oldestScope)
	}
}

// isRejectFeedback 是可单测的词表判断，独立出来避免和 TTL 逻辑缠在一起。
func isRejectFeedback(normalized string) bool {
	for _, w := range []string{"不对", "不是这个", "答非所问", "重新回答"} {
		if containsAny(normalized, w) {
			return true
		}
	}
	return false
}
