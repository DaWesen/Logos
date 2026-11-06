package scache

import (
	"sort"
	"sync"
)

// Candidate 是一条检索结果。
type Candidate struct {
	Entry      *Entry
	Similarity float64
}

// index 是进程内向量索引：每个 scope 一个 map，检索就是遍历 + 余弦。
//
// 为什么不上 HNSW：单 scope 上限 200 条，暴力扫约 20 万次乘加，
// 单核 0.2ms 量级；HNSW 的图遍历与内存碎片在这个规模反而更慢。
// 等指标证明不够再换，而不是提前为 1 万条的场景付复杂度。
type index struct {
	mu          sync.RWMutex
	buckets     map[string]map[string]*Entry
	total       int
	bytes       int64
	maxPerScope int
}

func newIndex(maxPerScope int) *index {
	return &index{
		buckets:     make(map[string]map[string]*Entry),
		maxPerScope: maxPerScope,
	}
}

// insert 写入条目并返回因超限被挤出的旧条目。
// 同一 entryID 重复写入按更新处理，不重复计数。
func (i *index) insert(e *Entry) []*Entry {
	i.mu.Lock()
	defer i.mu.Unlock()

	bucket := i.buckets[e.ScopeHash]
	if bucket == nil {
		bucket = make(map[string]*Entry)
		i.buckets[e.ScopeHash] = bucket
	}
	if old, ok := bucket[e.ID]; ok {
		i.total--
		i.bytes -= old.SizeBytes()
	}
	bucket[e.ID] = e
	i.total++
	i.bytes += e.SizeBytes()

	var evicted []*Entry
	if len(bucket) > i.maxPerScope {
		for len(bucket) > i.maxPerScope {
			oldest := oldestIn(bucket, nil)
			if oldest == nil {
				break
			}
			delete(bucket, oldest.ID)
			i.total--
			i.bytes -= oldest.SizeBytes()
			evicted = append(evicted, oldest)
		}
	}
	if len(bucket) == 0 {
		delete(i.buckets, e.ScopeHash)
	}
	return evicted
}

// get 返回条目快照。返回副本而不是指针：读路径会并发调用 touch 修改 Hits，
// 共享同一个结构体会触发 data race。
func (i *index) get(scopeHash, id string) *Entry {
	i.mu.RLock()
	defer i.mu.RUnlock()
	bucket := i.buckets[scopeHash]
	if bucket == nil {
		return nil
	}
	e, ok := bucket[id]
	if !ok {
		return nil
	}
	cp := *e
	return &cp
}

// search 返回 top-k。分数相同时按 CreatedAt 新者优先，便于命中刚写入的条目。
func (i *index) search(scopeHash string, vec []float32, k int, now int64) []Candidate {
	i.mu.RLock()
	defer i.mu.RUnlock()

	bucket := i.buckets[scopeHash]
	if len(bucket) == 0 {
		return nil
	}
	out := make([]Candidate, 0, len(bucket))
	for _, e := range bucket {
		if e.Expired(now) || !e.Valid(scopeHash, len(vec)) {
			continue
		}
		cp := *e
		out = append(out, Candidate{Entry: &cp, Similarity: CosineSimilarity(vec, e.Vec)})
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a].Similarity == out[b].Similarity {
			return out[a].Entry.CreatedAt > out[b].Entry.CreatedAt
		}
		return out[a].Similarity > out[b].Similarity
	})
	if len(out) > k {
		out = out[:k]
	}
	return out
}

// exact 在单个 scope 内按归一化文本查找。
//
// 单 scope 最多 200 条，字符串比较是微秒级；单独维护一张精确索引 map
// 要多一份写入/淘汰的一致性负担，收益不值得。命中后仍然返回副本，
// 避免调用方与 touch 并发修改同一个结构体。
func (i *index) exact(scopeHash, normQuery string, now int64) *Entry {
	i.mu.RLock()
	defer i.mu.RUnlock()

	bucket := i.buckets[scopeHash]
	if len(bucket) == 0 {
		return nil
	}
	var out *Entry
	for _, e := range bucket {
		if e.Expired(now) || e.NormQuery != normQuery {
			continue
		}
		if out == nil || e.CreatedAt > out.CreatedAt {
			out = e
		}
	}
	if out == nil {
		return nil
	}
	cp := *out
	return &cp
}

// touch 记录一次命中。Hits 只用于观测与后续 TTL 调优，不参与命中判定。
func (i *index) touch(scopeHash, id string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if bucket := i.buckets[scopeHash]; bucket != nil {
		if e := bucket[id]; e != nil {
			e.Hits++
		}
	}
}

func (i *index) remove(scopeHash, id string) *Entry {
	i.mu.Lock()
	defer i.mu.Unlock()
	bucket := i.buckets[scopeHash]
	if bucket == nil {
		return nil
	}
	e, ok := bucket[id]
	if !ok {
		return nil
	}
	delete(bucket, id)
	i.total--
	i.bytes -= e.SizeBytes()
	if len(bucket) == 0 {
		delete(i.buckets, scopeHash)
	}
	return e
}

// removeScope 删除整个 scope，供用户级「忘记我」使用。
func (i *index) removeScope(scopeHash string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	bucket := i.buckets[scopeHash]
	if bucket == nil {
		return
	}
	for _, e := range bucket {
		i.total--
		i.bytes -= e.SizeBytes()
	}
	delete(i.buckets, scopeHash)
}

// oldest 返回全局最旧条目，供后台淘汰使用。调用频率是分钟级，
// O(total) 扫描不会进入请求路径（禁区 X11）。
func (i *index) oldest() *Entry {
	i.mu.RLock()
	defer i.mu.RUnlock()
	var out *Entry
	for _, bucket := range i.buckets {
		for _, e := range bucket {
			if out == nil || e.CreatedAt < out.CreatedAt {
				out = e
			}
		}
	}
	if out == nil {
		return nil
	}
	cp := *out
	return &cp
}

func (i *index) stats() (total int, bytes int64) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.total, i.bytes
}

func (i *index) scopeCount() int {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return len(i.buckets)
}

func oldestIn(bucket map[string]*Entry, skip map[string]struct{}) *Entry {
	var out *Entry
	for id, e := range bucket {
		if skip != nil {
			if _, ok := skip[id]; ok {
				continue
			}
		}
		if out == nil || e.CreatedAt < out.CreatedAt {
			out = e
		}
	}
	return out
}
