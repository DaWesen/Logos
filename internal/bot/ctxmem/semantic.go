package ctxmem

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
)

// Slot 一条会话内结构化事实。
//
// 与跨会话的 UserMemory 刻意分开：订单号只对本次对话有效，
// 写进长期记忆会让下个月的提问被三个月前的旧订单污染。
type Slot struct {
	ID             string
	ConversationID string
	Key            string
	Value          string
	Kind           string
	SourceMsgID    string
	SourceRole     string
	TurnIndex      int
	SupersededBy   string
	Confidence     float64
	CreatedAt      time.Time
}

// SourceMessage 规则抽取的输入。
type SourceMessage struct {
	ID        string
	Role      string
	Content   string
	TurnIndex int
}

var (
	phonePattern  = regexp.MustCompile(`(?:^|[^0-9])(1[3-9][0-9]{9})(?:[^0-9]|$)`)
	emailPattern  = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)
	amountPattern = regexp.MustCompile(`(?:¥|￥|\$)\s?[0-9][0-9,]*(?:\.[0-9]{1,2})?|[0-9][0-9,]*(?:\.[0-9]{1,2})?\s?(?:元|块钱|块|美元)`)
	isoDate       = regexp.MustCompile(`\b20[0-9]{2}-[0-9]{1,2}-[0-9]{1,2}\b`)
	cnDate        = regexp.MustCompile(`[0-9]{1,2}月[0-9]{1,2}日`)
	urlPattern    = regexp.MustCompile(`https?://[^\s，。；、）)]+`)
)

var orderContextWords = []string{"订单号", "订单编号", "单号", "编号", "订单"}

// 优先级只影响注入顺序与预算裁剪，不参与事实真假裁决。
var slotPriority = map[string]int{
	"order_id": 0,
	"phone":    1,
	"email":    2,
	"amount":   3,
	"date":     4,
	"url":      5,
	"entity":   6,
}

var slotLabel = map[string]string{
	"order_id": "订单号",
	"phone":    "电话",
	"email":    "邮箱",
	"amount":   "金额",
	"date":     "日期",
	"url":      "链接",
	"entity":   "实体",
}

// ExtractSlots 用规则抽取有固定形态的事实。
//
// 规则优先的三个理由：零成本、可单测、不会把订单号抄错一位。
// 模型只应负责 entity 这种没有固定形态的类别，P1 暂不接入。
func ExtractSlots(msg SourceMessage) []Slot {
	if strings.TrimSpace(msg.Content) == "" {
		return nil
	}

	seen := make(map[string]struct{})
	slots := make([]Slot, 0, 4)
	add := func(key, value, kind string) {
		value = strings.TrimSpace(value)
		if value == "" || !strings.Contains(msg.Content, value) {
			// I3：值必须是源消息子串。规则路正常不会失败，
			// 这里保留校验是为了让未来模型路复用同一入口。
			return
		}
		dedupeKey := key + "\x00" + value
		if _, ok := seen[dedupeKey]; ok {
			return
		}
		seen[dedupeKey] = struct{}{}
		slots = append(slots, Slot{
			Key:         key,
			Value:       value,
			Kind:        kind,
			SourceMsgID: msg.ID,
			SourceRole:  msg.Role,
			TurnIndex:   msg.TurnIndex,
			Confidence:  1,
			CreatedAt:   time.Time{},
		})
	}

	for _, value := range extractOrderIDs(msg.Content) {
		add("order_id", value, "id")
	}
	for _, match := range phonePattern.FindAllStringSubmatch(msg.Content, -1) {
		add("phone", match[1], "id")
	}
	for _, value := range emailPattern.FindAllString(msg.Content, -1) {
		add("email", value, "id")
	}
	for _, value := range amountPattern.FindAllString(msg.Content, -1) {
		add("amount", value, "amount")
	}
	for _, value := range isoDate.FindAllString(msg.Content, -1) {
		add("date", value, "date")
	}
	for _, value := range cnDate.FindAllString(msg.Content, -1) {
		add("date", value, "date")
	}
	for _, value := range urlPattern.FindAllString(msg.Content, -1) {
		add("url", strings.TrimRight(value, ".,;，。；"), "url")
	}

	sortSlots(slots)
	return slots
}

func extractOrderIDs(content string) []string {
	var out []string
	for _, contextWord := range orderContextWords {
		offset := 0
		for {
			pos := strings.Index(content[offset:], contextWord)
			if pos < 0 {
				break
			}
			start := offset + pos + len(contextWord)
			token, end := orderTokenAfter(content, start)
			if token != "" {
				out = append(out, token)
				offset = end
				continue
			}
			offset = start
		}
	}
	return out
}

// orderTokenAfter 跳过上下文词后的空白与标点，读取 10–24 位字母数字。
// 不用正则的 \b：中文与数字相邻时 Go 的 \b 不认为是词边界。
func orderTokenAfter(content string, start int) (string, int) {
	runes := []rune(content)
	byteIndex := len([]rune(content[:start]))
	i := byteIndex
	skipped := 0
	for i < len(runes) && skipped < 8 {
		r := runes[i]
		if unicode.IsSpace(r) || strings.ContainsRune("是：:，,。.为", r) {
			i++
			skipped++
			continue
		}
		break
	}

	tokenStart := i
	for i < len(runes) && isOrderRune(runes[i]) {
		i++
	}
	if i-tokenStart < 10 || i-tokenStart > 24 {
		return "", start
	}
	token := string(runes[tokenStart:i])
	if !containsDigit(token) {
		return "", start
	}
	return token, len(string(runes[:i]))
}

func isOrderRune(r rune) bool {
	return r < 128 && (unicode.IsDigit(r) || unicode.IsLetter(r))
}

func containsDigit(s string) bool {
	for _, r := range s {
		if unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

// ResolveSlot 决定新槽位如何写入：是否需要作废旧值、是否应整体跳过。
//
// 只有三种合法处理（规格 §8.3）：
//  1. 更正：同来源 user，且新值来自更晚的轮次；
//  2. 并存：来源不同（user vs tool），注入时并列标注；
//  3. 歧义：同一轮内出现两个值，两条都保留。
//
// 禁止按 Confidence 裁决：置信度只表示「我有多确定听到了」，
// 不表示「哪个值是真的」。
func ResolveSlot(current []Slot, incoming Slot) (supersedeIDs []string, skip bool) {
	for _, slot := range current {
		if slot.Key != incoming.Key || slot.SupersededBy != "" {
			continue
		}
		if slot.Value == incoming.Value {
			return nil, true
		}
		switch {
		case slot.SourceRole == "user" && incoming.SourceRole == "user":
			if slot.TurnIndex < incoming.TurnIndex {
				supersedeIDs = append(supersedeIDs, slot.ID)
			}
		case slot.SourceRole == "tool" && incoming.SourceRole == "tool":
			// 新一次工具查询结果比旧结果新，视为刷新。
			if slot.TurnIndex < incoming.TurnIndex {
				supersedeIDs = append(supersedeIDs, slot.ID)
			}
		}
	}
	sort.Strings(supersedeIDs)
	return supersedeIDs, false
}

// RenderSlots 把事实槽渲染成注入文本，并在预算内按优先级裁剪。
//
// 冲突提示必须保留：只给最终值会让模型无法理解用户为什么记得另一个数，
// 用户再次提到旧值时回答会前后不一致。
func RenderSlots(slots []Slot, budget int, est TokenEstimator) (string, []Omission) {
	if len(slots) == 0 || budget <= 0 {
		return "", nil
	}
	if est == nil {
		est = DefaultEstimator{}
	}

	byID := make(map[string]Slot, len(slots))
	current := make([]Slot, 0, len(slots))
	superseded := make([]Slot, 0, len(slots))
	for _, slot := range slots {
		byID[slot.ID] = slot
		if slot.SupersededBy == "" {
			current = append(current, slot)
		} else {
			superseded = append(superseded, slot)
		}
	}

	sortSlots(current)
	sort.Slice(superseded, func(i, j int) bool {
		if superseded[i].Key != superseded[j].Key {
			return slotPriorityOf(superseded[i].Key) < slotPriorityOf(superseded[j].Key)
		}
		if superseded[i].TurnIndex != superseded[j].TurnIndex {
			return superseded[i].TurnIndex < superseded[j].TurnIndex
		}
		return superseded[i].ID < superseded[j].ID
	})

	lines := []string{"【本次会话已确认的事实】以下事实摘自本次会话的原始消息，可直接作为回答依据："}
	omitted := 0
	for _, slot := range current {
		line := fmt.Sprintf("- %s：%s（第 %d 轮，%s）",
			slotLabelOf(slot.Key), slot.Value, slot.TurnIndex, sourceLabel(slot.SourceRole))
		candidate := append(lines, line)
		if est.Estimate(strings.Join(candidate, "\n")) > budget {
			omitted++
			continue
		}
		lines = candidate
	}

	seenNotes := make(map[string]struct{})
	for _, old := range superseded {
		if old.SourceRole != "user" {
			continue
		}
		newer, ok := byID[old.SupersededBy]
		if !ok || newer.SourceRole != "user" || old.TurnIndex >= newer.TurnIndex {
			continue
		}
		line := fmt.Sprintf("- 注意：用户在第 %d 轮曾把%s说成 %s，已在第 %d 轮更正为 %s。",
			old.TurnIndex, slotLabelOf(old.Key), old.Value, newer.TurnIndex, newer.Value)
		if _, ok := seenNotes[line]; ok {
			continue
		}
		candidate := append(lines, line)
		if est.Estimate(strings.Join(candidate, "\n")) > budget {
			omitted++
			continue
		}
		seenNotes[line] = struct{}{}
		lines = candidate
	}

	if len(lines) == 1 {
		return "", []Omission{{Layer: LayerSemantic, Reason: "budget", Count: omitted}}
	}

	var omissions []Omission
	if omitted > 0 {
		omissions = append(omissions, Omission{Layer: LayerSemantic, Reason: "budget", Count: omitted})
	}
	return strings.Join(lines, "\n"), omissions
}

func sortSlots(slots []Slot) {
	sort.Slice(slots, func(i, j int) bool {
		if slotPriorityOf(slots[i].Key) != slotPriorityOf(slots[j].Key) {
			return slotPriorityOf(slots[i].Key) < slotPriorityOf(slots[j].Key)
		}
		if slots[i].TurnIndex != slots[j].TurnIndex {
			return slots[i].TurnIndex < slots[j].TurnIndex
		}
		if slots[i].Value != slots[j].Value {
			return slots[i].Value < slots[j].Value
		}
		return slots[i].ID < slots[j].ID
	})
}

func slotPriorityOf(key string) int {
	if priority, ok := slotPriority[key]; ok {
		return priority
	}
	return len(slotPriority)
}

func slotLabelOf(key string) string {
	if label, ok := slotLabel[key]; ok {
		return label
	}
	return key
}

func sourceLabel(role string) string {
	switch role {
	case "user":
		return "用户提供"
	case "assistant":
		return "助手消息"
	case "tool":
		return "工具查询结果"
	default:
		if role == "" {
			return "未知来源"
		}
		return role
	}
}
