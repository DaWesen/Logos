package ctxmem

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

// ==================== 会话内检索 ====================
//
// 这一层只解决一个问题：用户说「那个订单」时，把第 3 轮的原文找回来。
// 不引入向量检索是刻意的选择——订单号 20261106001 和 20261106002 的
// embedding 距离极小，但它们是完全不同的两个订单。这里要的是精确匹配，
// 不是语义相似；BM25 还能解释「因为命中了 订单/20261106001」。

const (
	bm25K1 = 1.2
	bm25B  = 0.75
	rrfK   = 60.0
	// slotRouteWeight 槽位精确匹配路的权重。精确命中本来就该赢，
	// 否则一条恰好包含「订单」两个字的寒暄会把真正的订单消息挤下去。
	slotRouteWeight = 2.0
	// recallLimit 先召回 8 条给重排留余量，注入时再按预算截到 3 条。
	recallLimit = 8
	maxInject   = 3
)

// Doc 索引中的一条消息。
type Doc struct {
	MsgID     string
	TurnIndex int
	Role      string
	Content   string
	Len       int
}

// Index 会话级 bigram 倒排索引。
//
// 中文不做分词：引入分词器就引入一个依赖和一个需要维护的词典，
// 而字符二元组对「订单号」「退款」这类词已经足够。
type Index struct {
	docs   map[string]*Doc
	terms  map[string][]string
	avgLen float64
}

// BuildIndex 为一段按时间正序的消息建索引。
// 调用方负责在会话消息变化时整体重建，而不是增量修改：
// 增量维护倒排的复杂度远高于 O(消息数) 的重建。
func BuildIndex(messages []Message) *Index {
	index := &Index{
		docs:  make(map[string]*Doc, len(messages)),
		terms: make(map[string][]string),
	}
	if len(messages) == 0 {
		return index
	}

	totalLen := 0
	for _, message := range messages {
		if message.ID == "" || strings.TrimSpace(message.Content) == "" {
			continue
		}
		terms := bigramTerms(message.Content)
		if len(terms) == 0 {
			continue
		}
		doc := &Doc{
			MsgID:     message.ID,
			TurnIndex: message.TurnIndex,
			Role:      message.Role,
			Content:   message.Content,
			Len:       len(terms),
		}
		index.docs[message.ID] = doc
		totalLen += doc.Len
		seen := make(map[string]struct{}, len(terms))
		for _, term := range terms {
			if _, ok := seen[term]; ok {
				continue
			}
			seen[term] = struct{}{}
			index.terms[term] = append(index.terms[term], message.ID)
		}
	}
	if len(index.docs) > 0 {
		index.avgLen = float64(totalLen) / float64(len(index.docs))
	}
	return index
}

// bigramTerms 把文本切成字符二元组。
//
// 标点与空白不作为词的组成部分，避免生成「。订」这种只在两边都出现
// 相同标点时才命中的噪声 token。ASCII 字母统一转小写。
func bigramTerms(text string) []string {
	runes := make([]rune, 0, len(text))
	for _, r := range strings.ToLower(text) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			runes = append(runes, r)
			continue
		}
		// 标点/空白是天然边界：先冲刷当前片段，再继续。
		if len(runes) > 0 {
			// 用一个不可能出现在文本里的分隔符，交给下面的切片逻辑处理。
			runes = append(runes, 0)
		}
	}

	terms := make([]string, 0, len(runes))
	segment := make([]rune, 0, 8)
	flush := func() {
		if len(segment) == 1 {
			terms = append(terms, string(segment))
		}
		for i := 0; i+1 < len(segment); i++ {
			terms = append(terms, string(segment[i:i+2]))
		}
		segment = segment[:0]
	}
	for _, r := range runes {
		if r == 0 {
			flush()
			continue
		}
		segment = append(segment, r)
	}
	flush()
	return terms
}

// Hit 一条召回结果。
type Hit struct {
	Doc   *Doc
	Score float64
	Route string // bm25 / slot / both
}

// Trigger 检索触发规则。
type Trigger struct {
	Rule  string // T1 / T2 / T3
	Query string
}

var pronounWords = []string{
	"那个", "这个", "它", "他", "她", "上次", "之前", "刚才", "前面", "那条", "我说的",
}

var backfillWords = []string{
	"翻一下", "翻翻", "前面说", "之前说", "上次说", "上次讲", "刚才说", "回顾一下", "找一下之前",
}

// DetectTrigger 判断本轮是否需要检索。
//
// 不调用模型判断：触发规则必须是确定性的，否则同一句话在不同轮次
// 可能走不同路径，线上无法复现，也无法解释「为什么这轮没召回」。
func DetectTrigger(query string, slots []Slot) (Trigger, bool) {
	query = strings.TrimSpace(query)
	if query == "" {
		return Trigger{}, false
	}
	lower := strings.ToLower(query)

	for _, word := range backfillWords {
		if strings.Contains(lower, word) {
			return Trigger{Rule: "T3", Query: query}, true
		}
	}

	// T2：出现了 ID 形态的数字/字母串，但事实槽里没有这个值。
	// 这是「用户报了一个新单号」的场景，原文里可能已经出现过同号。
	for _, token := range idLikeTokens(query) {
		if !slotContainsValue(slots, token) {
			return Trigger{Rule: "T2", Query: query}, true
		}
	}

	// T1：有指代词，且槽位里有可指向的对象。
	// 槽位为空时「那个订单」没有可检索的锚点，只能退化为关键词检索，
	// 效果差且容易召回噪音，所以不触发。
	if len(slots) > 0 {
		for _, word := range pronounWords {
			if strings.Contains(lower, word) {
				return Trigger{Rule: "T1", Query: query}, true
			}
		}
	}
	return Trigger{}, false
}

// idLikeTokens 提取长度 >= 6 的连续 ASCII 字母数字串，且必须含数字。
// 6 是经验阈值：订单号通常 >= 10 位，但「2026-11-06」这类日期
// 拆开后每段不足 6 位，不会误触发。
func idLikeTokens(query string) []string {
	var out []string
	var current []rune
	flush := func() {
		if len(current) >= 6 && containsDigit(string(current)) {
			out = append(out, string(current))
		}
		current = current[:0]
	}
	for _, r := range query {
		// ID 只认 ASCII：中文句子也是连续字母，不能当单号。
		if r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			current = append(current, r)
			continue
		}
		flush()
	}
	flush()
	return out
}

func slotContainsValue(slots []Slot, value string) bool {
	for _, slot := range slots {
		if slot.Value == value {
			return true
		}
	}
	return false
}

// Search 执行 BM25 + 槽位精确匹配两路召回，用 RRF 融合。
//
// 返回结果按分数降序，最多 recallLimit 条。
func (index *Index) Search(query string, slots []Slot) []Hit {
	if index == nil || len(index.docs) == 0 {
		return nil
	}

	queryTerms := bigramTerms(query)
	for _, slot := range slots {
		if slot.SupersededBy != "" {
			continue
		}
		queryTerms = append(queryTerms, bigramTerms(slot.Value)...)
	}

	bm25 := index.scoreBM25(queryTerms)
	slotRoute := index.scoreSlots(slots)

	rankA := rankMap(bm25)
	rankB := rankMap(slotRoute)
	scores := make(map[string]float64, len(rankA)+len(rankB))
	routes := make(map[string]string, len(rankA)+len(rankB))
	for id, rank := range rankA {
		scores[id] += 1.0 / (rrfK + float64(rank+1))
		routes[id] = "bm25"
	}
	for id, rank := range rankB {
		scores[id] += slotRouteWeight / (rrfK + float64(rank+1))
		if routes[id] == "bm25" {
			routes[id] = "both"
		} else {
			routes[id] = "slot"
		}
	}

	hits := make([]Hit, 0, len(scores))
	for id, score := range scores {
		doc := index.docs[id]
		if doc == nil {
			continue
		}
		hits = append(hits, Hit{Doc: doc, Score: score, Route: routes[id]})
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		if hits[i].Doc.TurnIndex != hits[j].Doc.TurnIndex {
			return hits[i].Doc.TurnIndex > hits[j].Doc.TurnIndex
		}
		return hits[i].Doc.MsgID < hits[j].Doc.MsgID
	})
	if len(hits) > recallLimit {
		hits = hits[:recallLimit]
	}
	return hits
}

// scoreBM25 对查询词做 BM25 打分。返回 msgID -> score。
func (index *Index) scoreBM25(queryTerms []string) map[string]float64 {
	tf := make(map[string]map[string]int, len(index.docs))
	// 逐文档统计词频：语料是单会话几百到几千条，倒排表已经能快速
	// 定位候选文档，这里只对候选计分，不扫全量。
	candidates := make(map[string]struct{})
	seenTerms := make(map[string]struct{}, len(queryTerms))
	for _, term := range queryTerms {
		if _, ok := seenTerms[term]; ok {
			continue
		}
		seenTerms[term] = struct{}{}
		for _, id := range index.terms[term] {
			candidates[id] = struct{}{}
		}
	}

	scores := make(map[string]float64, len(candidates))
	for id := range candidates {
		doc := index.docs[id]
		if doc == nil {
			continue
		}
		if tf[id] == nil {
			tf[id] = termFreq(doc.Content)
		}
		for term := range seenTerms {
			freq := tf[id][term]
			if freq == 0 {
				continue
			}
			df := len(index.terms[term])
			idf := math.Log(1 + (float64(len(index.docs))-float64(df)+0.5)/(float64(df)+0.5))
			denom := float64(freq) + bm25K1*(1-bm25B+bm25B*float64(doc.Len)/maxFloat(index.avgLen, 1))
			scores[id] += idf * float64(freq) * (bm25K1 + 1) / denom
		}
	}
	return scores
}

// scoreSlots 把「消息里出现了当前槽位值」的文档置顶。
// 按轮次从新到旧排名：同一个订单号被多次提及时，最近一次的上下文
// 通常才是用户想指的「那个」。
func (index *Index) scoreSlots(slots []Slot) map[string]float64 {
	values := make([]string, 0, len(slots))
	for _, slot := range slots {
		if slot.SupersededBy != "" || strings.TrimSpace(slot.Value) == "" {
			continue
		}
		values = append(values, slot.Value)
	}
	if len(values) == 0 {
		return nil
	}

	scores := make(map[string]float64)
	for id, doc := range index.docs {
		for _, value := range values {
			if strings.Contains(doc.Content, value) {
				scores[id] = float64(doc.TurnIndex)
				break
			}
		}
	}
	return scores
}

func rankMap(scores map[string]float64) map[string]int {
	type pair struct {
		id    string
		score float64
	}
	pairs := make([]pair, 0, len(scores))
	for id, score := range scores {
		pairs = append(pairs, pair{id: id, score: score})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].score != pairs[j].score {
			return pairs[i].score > pairs[j].score
		}
		return pairs[i].id < pairs[j].id
	})
	ranks := make(map[string]int, len(pairs))
	for i, item := range pairs {
		ranks[item.id] = i
	}
	return ranks
}

func termFreq(content string) map[string]int {
	freq := make(map[string]int)
	for _, term := range bigramTerms(content) {
		freq[term]++
	}
	return freq
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// RenderHits 把召回结果渲染成带出处的注入文本。
//
// 没有出处的召回等于给模型一段来源不明的文本，它会当成「用户刚说的」，
// 从而产生「您刚才提到」这种错误归因。
func RenderHits(hits []Hit, budget int, excluded map[string]struct{}, est TokenEstimator) (string, []Omission) {
	if len(hits) == 0 || budget <= 0 {
		return "", nil
	}
	if est == nil {
		est = DefaultEstimator{}
	}

	lines := []string{
		"【历史原文片段】以下是本次会话中较早的原始消息，按相关度排序；它们是原文，不是摘要：",
		"【裁决规则】当「已确认的事实」与「对话摘要」不一致时，以「已确认的事实」为准；当「历史原文片段」与「对话摘要」不一致时，以原文为准。",
	}
	injected := 0
	omitted := 0
	for _, hit := range hits {
		if hit.Doc == nil {
			continue
		}
		if _, ok := excluded[hit.Doc.MsgID]; ok {
			continue
		}
		line := fmt.Sprintf("[第 %d 轮 · %s] %s",
			hit.Doc.TurnIndex, retrievalRoleLabel(hit.Doc.Role), hit.Doc.Content)
		candidate := append(lines, line)
		if est.Estimate(strings.Join(candidate, "\n")) > budget {
			omitted++
			continue
		}
		lines = candidate
		injected++
		if injected >= maxInject {
			break
		}
	}

	if injected == 0 {
		reason := "no_match"
		if omitted > 0 {
			reason = "budget"
		}
		return "", []Omission{{Layer: LayerRetrieval, Reason: reason, Count: maxInt(omitted, 1)}}
	}

	var omissions []Omission
	if omitted > 0 {
		omissions = append(omissions, Omission{Layer: LayerRetrieval, Reason: "budget", Count: omitted})
	}
	return strings.Join(lines, "\n"), omissions
}

func retrievalRoleLabel(role string) string {
	switch role {
	case "user":
		return "用户"
	case "assistant":
		return "助手"
	case "tool":
		return "工具"
	default:
		if role == "" {
			return "未知"
		}
		return role
	}
}

// ==================== 会话级索引缓存 ====================

// IndexCache 按会话缓存倒排索引。
//
// 第一次触发检索时建，之后 10 分钟内复用；会话有新消息时指纹变化，
// 下一次检索整体重建。不落库：重建是 O(消息数)，比维护持久化索引便宜。
type IndexCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	maxSize int
	entries map[string]indexEntry
	clock   func() time.Time
}

type indexEntry struct {
	index       *Index
	fingerprint string
	builtAt     time.Time
}

func NewIndexCache(ttl time.Duration, maxSize int) *IndexCache {
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	if maxSize <= 0 {
		maxSize = 64
	}
	return &IndexCache{
		ttl:     ttl,
		maxSize: maxSize,
		entries: make(map[string]indexEntry, maxSize),
		clock:   time.Now,
	}
}

// Get 返回缓存索引；指纹不一致或过期时返回 nil，由调用方重建后 Put。
func (c *IndexCache) Get(conversationID, fingerprint string) *Index {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[conversationID]
	if !ok || entry.fingerprint != fingerprint {
		return nil
	}
	if c.clock().Sub(entry.builtAt) > c.ttl {
		delete(c.entries, conversationID)
		return nil
	}
	return entry.index
}

// Put 写入索引并做最简单的 LRU：超过上限时删掉最早构建的一项。
// 会话级索引的访问有明显局部性，精确 LRU 的收益不值得多维护一条链表。
func (c *IndexCache) Put(conversationID, fingerprint string, index *Index) {
	if c == nil || index == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= c.maxSize {
		var oldestID string
		var oldest time.Time
		for id, entry := range c.entries {
			if oldestID == "" || entry.builtAt.Before(oldest) {
				oldestID, oldest = id, entry.builtAt
			}
		}
		if oldestID != "" {
			delete(c.entries, oldestID)
		}
	}
	c.entries[conversationID] = indexEntry{
		index:       index,
		fingerprint: fingerprint,
		builtAt:     c.clock(),
	}
}

// Fingerprint 取会话最新消息 ID 与条数作为索引版本。
// 只需要判断「有没有变化」，不需要内容哈希：消息只追加不修改。
func Fingerprint(messages []Message) string {
	if len(messages) == 0 {
		return "0"
	}
	return fmt.Sprintf("%d:%s", len(messages), messages[len(messages)-1].ID)
}
