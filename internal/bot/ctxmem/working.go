package ctxmem

import "time"

// Message 装配层看到的最小消息视图。
// 只保留预算与分轮需要的信息，避免 ctxmem 依赖具体存储模型。
type Message struct {
	ID        string
	Role      string
	Content   string
	CreatedAt time.Time
	TurnIndex int
}

// PackWorking 按「轮」从新到旧装载 Working 层。
//
// 一轮 = 一条 user + 其后所有 assistant/tool 消息。按条装载会把
// assistant(tool_calls) 和它的 tool 结果拆开，直接违反 I2。
//
// messages 必须按时间正序。返回的 kept/overflow 也保持正序。
// 若最新一轮单独就超预算，只保留该轮最后一条消息并做 rune-safe 截断：
// 当前问题不能丢，但 I1（Σ tokens ≤ H）是硬约束。
func PackWorking(messages []Message, budget int, est TokenEstimator) (kept, overflow []Message, omitted int) {
	if len(messages) == 0 {
		return nil, nil, 0
	}
	if est == nil {
		est = DefaultEstimator{}
	}
	if budget <= 0 {
		return nil, append([]Message(nil), messages...), len(messages)
	}

	groups := turnGroups(messages)
	used := 0
	firstKept := len(messages)

	for gi := len(groups) - 1; gi >= 0; gi-- {
		group := groups[gi]
		cost := messageRangeTokens(messages[group.start:group.end], est)
		if used+cost > budget {
			break
		}
		used += cost
		firstKept = group.start
	}

	if firstKept < len(messages) {
		return append([]Message(nil), messages[firstKept:]...),
			append([]Message(nil), messages[:firstKept]...),
			firstKept
	}

	// 最新一轮塞不进预算：保留这一轮的 user 消息并截断。
	//
	// 不能直接取 len-1：最新一条可能是 tool 结果，只留它就成了
	// 「没有对应 assistant(tool_calls) 的孤儿工具消息」，供应商会 400。
	// 保留问题本身既不破坏配对，也保住用户当下要问的事。
	keepAt := lastUserIndex(messages)
	last := messages[keepAt]
	last.Content = TruncateToTokens(last.Content, budget, est)
	if last.Content == "" {
		return nil, append([]Message(nil), messages...), len(messages)
	}
	overflow = make([]Message, 0, len(messages)-1)
	overflow = append(overflow, messages[:keepAt]...)
	overflow = append(overflow, messages[keepAt+1:]...)
	return []Message{last}, overflow, len(messages) - 1
}

// lastUserIndex 返回最后一条 user 消息的下标；整段没有 user 时退回最后一条。
func lastUserIndex(messages []Message) int {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			return i
		}
	}
	return len(messages) - 1
}

type turnGroup struct {
	start int
	end   int
}

func turnGroups(messages []Message) []turnGroup {
	groups := make([]turnGroup, 0, len(messages))
	for i, message := range messages {
		if message.Role == "user" || len(groups) == 0 {
			groups = append(groups, turnGroup{start: i, end: i + 1})
			continue
		}
		groups[len(groups)-1].end = i + 1
	}
	return groups
}

func messageRangeTokens(messages []Message, est TokenEstimator) int {
	total := 0
	for _, message := range messages {
		total += est.Estimate(message.Content)
	}
	return total
}

// MessagesTokens 估算一组消息的 token 总量。
func MessagesTokens(messages []Message, est TokenEstimator) int {
	if est == nil {
		est = DefaultEstimator{}
	}
	return messageRangeTokens(messages, est)
}

// TruncateToTokens 在 token 预算内做 rune-safe 截断。
// 二分查找保证不会从多字节字符中间切开，也不会因逐字重算退化为 O(n²)。
func TruncateToTokens(text string, budget int, est TokenEstimator) string {
	if budget <= 0 {
		return ""
	}
	if est == nil {
		est = DefaultEstimator{}
	}
	if est.Estimate(text) <= budget {
		return text
	}
	runes := []rune(text)
	low, high := 0, len(runes)
	for low < high {
		mid := (low + high + 1) / 2
		if est.Estimate(string(runes[:mid])) <= budget {
			low = mid
		} else {
			high = mid - 1
		}
	}
	if low == 0 {
		return ""
	}
	return string(runes[:low])
}
