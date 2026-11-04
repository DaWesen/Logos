package memory

import (
	"math"
	"sort"
	"strings"
	"time"
	"unicode"

	botmodel "Logos/internal/service/ai/bot/model"
)

// 记忆注入的检索策略（借鉴 Generative Agents 的三因子：相关性 / 新鲜度 / 重要性）。
//
// 旧实现只按 confidence 排序后全量注入：记忆一多，无关的旧偏好会挤掉与当前问题
// 真正相关的那条，而且越聊越容易被陈年记忆带偏。这里把「注入哪些记忆」从
// 排序问题变成检索问题。
const (
	// memoryPromptLimit 单次注入的记忆总条数上限。
	// 记忆不是越多越好：每条都在消耗上下文，无关记忆还会干扰模型。
	memoryPromptLimit = 16
	// memoryRecencyHalfLife 新鲜度半衰期：14 天。
	// 用户偏好会变（换口味、换城市），两周未更新的记忆权重减半。
	memoryRecencyHalfLife = 14 * 24 * time.Hour

	// 三个因子的权重。相关性最高，但置信度与新鲜度不能被完全淹没——
	// 一条 0.95 置信度的手工记忆，不该输给一条文字上碰巧重合的低质记忆。
	scoreWeightRelevance = 0.5
	scoreWeightRecency   = 0.3
	scoreWeightImportant = 0.2
)

// selectMemories 选出本次要注入的记忆。
//
// 规则：
//  1. 手工记忆（Source=manual）必须保留：用户明确要求记住的信息，
//     不能因为当前问题「文字上不相关」就被丢掉；
//  2. 其余按三因子分数降序，全序键保证同一输入输出稳定；
//  3. 总数不超过 memoryPromptLimit，超出部分由调用方声明条数。
func selectMemories(mems []*botmodel.UserMemory, query string, now time.Time) []*botmodel.UserMemory {
	if len(mems) == 0 {
		return nil
	}
	queryLower := strings.ToLower(strings.TrimSpace(query))

	pinned := make([]*botmodel.UserMemory, 0, len(mems))
	rest := make([]*botmodel.UserMemory, 0, len(mems))
	for _, mem := range mems {
		if mem == nil {
			continue
		}
		if mem.Source == "manual" {
			pinned = append(pinned, mem)
			continue
		}
		rest = append(rest, mem)
	}

	scores := make(map[*botmodel.UserMemory]float64, len(rest))
	for _, mem := range rest {
		scores[mem] = memoryScore(mem, queryLower, now)
	}
	sort.SliceStable(rest, func(i, j int) bool {
		a, b := rest[i], rest[j]
		if scores[a] != scores[b] {
			return scores[a] > scores[b]
		}
		// 同分全序兜底：置信度 → 更新时间 → ID
		if a.Confidence != b.Confidence {
			return a.Confidence > b.Confidence
		}
		if !a.UpdatedAt.Equal(b.UpdatedAt) {
			return a.UpdatedAt.After(b.UpdatedAt)
		}
		return a.ID < b.ID
	})
	sortMemories(pinned)

	selected := make([]*botmodel.UserMemory, 0, memoryPromptLimit)
	selected = append(selected, pinned...)
	for _, mem := range rest {
		if len(selected) >= memoryPromptLimit {
			break
		}
		selected = append(selected, mem)
	}
	return selected
}

// memorySimilarityThreshold 视为「同一条记忆」的键相似度下限。
//
// 取 0.8：只兜住「多一个语气词 / 语序微调」这类近乎同写的键；
// 「喜欢喝咖啡」与「喜欢咖啡豆」约 0.33，必须保持两条。
// 同义替换（「喜欢」→「爱」）token 重合度低，不在词法手段能解决的范围。
const memorySimilarityThreshold = 0.8

// memoryKeySimilarity 两个记忆键的 token Jaccard 相似度，取值 [0,1]。
func memoryKeySimilarity(a, b string) float64 {
	ta := memoryTokens(a)
	tb := memoryTokens(b)
	if len(ta) == 0 || len(tb) == 0 {
		return 0
	}
	set := make(map[string]struct{}, len(ta))
	for _, t := range ta {
		set[t] = struct{}{}
	}
	inter := 0
	for _, t := range tb {
		if _, ok := set[t]; ok {
			inter++
		}
	}
	union := len(ta) + len(tb) - inter
	return float64(inter) / float64(union)
}

// findSimilarMemoryIndex 在已有记忆中找「近乎同写」的那条，返回下标；找不到返回 -1。
//
// 归一化只能消除格式差异（空格、标点），多一个语气词、语序微调仍会各存一条。
// 这里用 token 相似度兜住这类重复，避免记忆表被近似条目占满。
// 只比较同分类：不同分类的同名键表达的往往是不同维度的信息。
func findSimilarMemoryIndex(mems []*botmodel.UserMemory, key, category string) int {
	type candidate struct {
		idx   int
		score float64
	}
	var best candidate
	best.idx = -1

	for i, mem := range mems {
		if mem == nil || mem.Key == key {
			continue
		}
		if mem.Category != category {
			continue
		}
		score := memoryKeySimilarity(key, mem.Key)
		if score < memorySimilarityThreshold {
			continue
		}
		// 同分时选置信度更高的，再看更新时间更新的，保证结果确定
		if best.idx == -1 || score > best.score ||
			(score == best.score && mem.Confidence > mems[best.idx].Confidence) ||
			(score == best.score && mem.Confidence == mems[best.idx].Confidence && mem.UpdatedAt.After(mems[best.idx].UpdatedAt)) {
			best = candidate{idx: i, score: score}
		}
	}
	return best.idx
}

// memoryScore 计算单条记忆的注入优先级，取值 [0,1]。
// query 为空时退化为「置信度 + 新鲜度」——没有对话上下文可依据。
func memoryScore(mem *botmodel.UserMemory, queryLower string, now time.Time) float64 {
	recency := recencyScore(mem.UpdatedAt, now)
	if queryLower == "" {
		return 0.65*clamp01(mem.Confidence) + 0.35*recency
	}
	relevance := memoryRelevance(mem, queryLower)
	return scoreWeightRelevance*relevance +
		scoreWeightRecency*recency +
		scoreWeightImportant*clamp01(mem.Confidence)
}

// recencyScore 指数衰减：0.5^(age/halfLife)。
// 用半衰期而不是硬截断，避免「差一天就完全消失」的悬崖效应。
func recencyScore(updatedAt, now time.Time) float64 {
	if updatedAt.IsZero() {
		// 时间未知时给中间值，既不当作最新，也不直接判死。
		return 0.5
	}
	age := now.Sub(updatedAt)
	if age <= 0 {
		return 1
	}
	return math.Pow(0.5, float64(age)/float64(memoryRecencyHalfLife))
}

// memoryRelevance 词法重合度：命中 token 数 / query token 数，取值 [0,1]。
//
// 不引入 embedding：记忆条目短、数量少，向量检索的收益在这个规模上
// 抵不过一次额外远程调用的延迟与失败面；词法重合已能区分「相关偏好」和「无关旧事」。
func memoryRelevance(mem *botmodel.UserMemory, queryLower string) float64 {
	tokens := memoryTokens(queryLower)
	if len(tokens) == 0 {
		return 0
	}
	haystack := strings.ToLower(mem.Key + " " + mem.Value)
	hits := 0
	for _, token := range tokens {
		if strings.Contains(haystack, token) {
			hits++
		}
	}
	return float64(hits) / float64(len(tokens))
}

// memoryTokens 把查询切成用于重合匹配的 token：
//   - 拉丁字母/数字按单词切；
//   - 汉字按 bigram 切（中文没有空格，单字太泛、整句太严）；
//   - 单字查询保留单字，否则「猫」永远匹配不到「养猫」。
func memoryTokens(s string) []string {
	// 统一小写：调用方传原句时 'P' 会落进 default 分支被当成分隔符，
	// 结果 "Python" 被切成 "ython"，这类错误在检索里只表现为「莫名其妙搜不到」。
	s = strings.ToLower(s)
	seen := make(map[string]struct{}, len(s))
	out := make([]string, 0, len(s))

	var word []rune
	var han []rune

	flushWord := func() {
		if len(word) == 0 {
			return
		}
		if _, ok := seen[string(word)]; !ok {
			seen[string(word)] = struct{}{}
			out = append(out, string(word))
		}
		word = word[:0]
	}
	flushHan := func() {
		if len(han) == 0 {
			return
		}
		if len(han) == 1 {
			if _, ok := seen[string(han)]; !ok {
				seen[string(han)] = struct{}{}
				out = append(out, string(han))
			}
		} else {
			for i := 0; i+1 < len(han); i++ {
				bigram := string(han[i : i+2])
				if _, ok := seen[bigram]; !ok {
					seen[bigram] = struct{}{}
					out = append(out, bigram)
				}
			}
		}
		han = han[:0]
	}

	for _, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			flushHan()
			word = append(word, r)
		case unicode.Is(unicode.Han, r):
			flushWord()
			han = append(han, r)
		default:
			flushWord()
			flushHan()
		}
	}
	flushWord()
	flushHan()
	return out
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
