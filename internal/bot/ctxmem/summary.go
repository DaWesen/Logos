package ctxmem

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// ==================== 结构化摘要 ====================
//
// 自由文本摘要最大的问题不是「写得不好」，而是「无法验证」：
// 模型把 1280 写成 1820，没有任何环节能发现。结构化摘要把 facts
// 变成可逐条校验的字段——value 必须是原文子串，否则丢弃并计数。
// 这样「摘要可信」才是一个可证伪的陈述。

const (
	// SummarySchemaVersion 结构版本。格式变更时递增，旧版本按纯文本兼容。
	SummarySchemaVersion = 2
	// SummaryMaxFacts 事实条数上限。超过后按 evidence 的新旧与数量裁剪。
	SummaryMaxFacts = 50
	// SummaryBackgroundTokenLimit background 的 token 上限。
	SummaryBackgroundTokenLimit = 400
)

// SummaryFact 一条被摘录的事实。
// Evidence 是原文消息序号，用于回答「这句话从哪来的」。
type SummaryFact struct {
	Key      string `json:"key"`
	Value    string `json:"value"`
	Evidence []int  `json:"evidence,omitempty"`
}

// StructuredSummary 摘要的 JSON 契约。
type StructuredSummary struct {
	Background string        `json:"background"`
	Facts      []SummaryFact `json:"facts"`
	Decisions  []string      `json:"decisions"`
	OpenItems  []string      `json:"open_items"`
	Rejected   []string      `json:"rejected"`
}

// Empty 判断是否没有任何可注入内容。
func (s StructuredSummary) Empty() bool {
	return strings.TrimSpace(s.Background) == "" &&
		len(s.Facts) == 0 && len(s.Decisions) == 0 &&
		len(s.OpenItems) == 0 && len(s.Rejected) == 0
}

// ParseStructuredSummary 解析模型输出。
//
// 模型经常在 JSON 外包一层 markdown 代码块或解释文字，这里只截取
// 第一个 { 到最后一个 }；解析失败返回的错误由调用方按 L1 降级处理。
func ParseStructuredSummary(raw string) (StructuredSummary, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return StructuredSummary{}, fmt.Errorf("ctxmem: 摘要为空")
	}
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start < 0 || end <= start {
		return StructuredSummary{}, fmt.Errorf("ctxmem: 摘要不是 JSON 对象")
	}

	var summary StructuredSummary
	if err := json.Unmarshal([]byte(raw[start:end+1]), &summary); err != nil {
		return StructuredSummary{}, fmt.Errorf("ctxmem: 解析摘要 JSON: %w", err)
	}
	summary.normalize()
	return summary, nil
}

func (s *StructuredSummary) normalize() {
	s.Background = strings.TrimSpace(s.Background)
	s.Decisions = trimList(s.Decisions)
	s.OpenItems = trimList(s.OpenItems)
	s.Rejected = trimList(s.Rejected)

	facts := s.Facts[:0]
	for _, fact := range s.Facts {
		fact.Key = strings.TrimSpace(fact.Key)
		fact.Value = strings.TrimSpace(fact.Value)
		if fact.Value == "" {
			continue
		}
		if fact.Key == "" {
			fact.Key = "entity"
		}
		facts = append(facts, fact)
	}
	s.Facts = facts
}

func trimList(items []string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

// ValidateFacts 只保留 value 能在原文语料中逐字找到的事实。
//
// 返回被丢弃的 key 列表，调用方用它打点 ctx.summary_fact.drop。
// 这里不做模糊匹配：1280 与 1820 的编辑距离很小，但业务含义完全不同。
func ValidateFacts(facts []SummaryFact, corpus string) (kept []SummaryFact, dropped []string) {
	for _, fact := range facts {
		if fact.Value == "" || !strings.Contains(corpus, fact.Value) {
			dropped = append(dropped, fact.Key)
			continue
		}
		kept = append(kept, fact)
	}
	return kept, dropped
}

// TrimFacts 把事实裁剪到上限。
//
// 排序键是「最近一次 evidence 轮次」优先，其次 evidence 数量：
// 最近被提到的事实更可能与当前问题相关，被多次提到的更可能是关键事实。
// 相同分数时按 key/value 排序，保证输出确定（I5）。
func TrimFacts(facts []SummaryFact, limit int) []SummaryFact {
	if limit <= 0 || len(facts) <= limit {
		return facts
	}
	sorted := append([]SummaryFact(nil), facts...)
	sort.SliceStable(sorted, func(i, j int) bool {
		leftTurn, leftCount := evidenceStats(sorted[i].Evidence)
		rightTurn, rightCount := evidenceStats(sorted[j].Evidence)
		if leftTurn != rightTurn {
			return leftTurn > rightTurn
		}
		if leftCount != rightCount {
			return leftCount > rightCount
		}
		if sorted[i].Key != sorted[j].Key {
			return sorted[i].Key < sorted[j].Key
		}
		return sorted[i].Value < sorted[j].Value
	})
	return sorted[:limit]
}

func evidenceStats(evidence []int) (latest, count int) {
	count = len(evidence)
	for _, turn := range evidence {
		if turn > latest {
			latest = turn
		}
	}
	return latest, count
}

// RenderStructured 把结构化摘要渲染成注入文本，并在预算内按优先级裁剪。
//
// 裁剪顺序 rejected → decisions → open_items → background，facts 最后：
// 「用户否掉的方案」价值最低，「已确认事实」最高，任何情况下都不先丢。
func RenderStructured(summary StructuredSummary, budget int, est TokenEstimator) (string, []Omission) {
	if budget <= 0 || summary.Empty() {
		return "", nil
	}
	if est == nil {
		est = DefaultEstimator{}
	}

	if est.Estimate(summary.Background) > SummaryBackgroundTokenLimit {
		summary.Background = TruncateToTokens(summary.Background, SummaryBackgroundTokenLimit, est)
	}

	omitted := 0
	render := func() string {
		return renderSummaryBody(summary)
	}
	if est.Estimate(render()) <= budget {
		return render(), nil
	}

	// 先整段丢弃低价值列表，再截背景，最后才动 facts。
	for _, field := range []string{"rejected", "decisions", "open_items"} {
		if len(summary.Rejected)+len(summary.Decisions)+len(summary.OpenItems) == 0 {
			break
		}
		switch field {
		case "rejected":
			omitted += len(summary.Rejected)
			summary.Rejected = nil
		case "decisions":
			omitted += len(summary.Decisions)
			summary.Decisions = nil
		case "open_items":
			omitted += len(summary.OpenItems)
			summary.OpenItems = nil
		}
		if est.Estimate(render()) <= budget {
			return render(), budgetOmission(omitted)
		}
	}

	if summary.Background != "" {
		remaining := budget - (est.Estimate(render()) - est.Estimate(summary.Background))
		summary.Background = TruncateToTokens(summary.Background, maxInt(remaining, 0), est)
	}
	if est.Estimate(render()) <= budget {
		return render(), budgetOmission(omitted)
	}

	// facts 是最后防线：从末尾（分数最低）开始丢，至少保留第一条。
	for len(summary.Facts) > 1 && est.Estimate(render()) > budget {
		summary.Facts = summary.Facts[:len(summary.Facts)-1]
		omitted++
	}
	if est.Estimate(render()) > budget {
		return "", []Omission{{Layer: LayerEpisodic, Reason: "budget", Count: maxInt(omitted, 1)}}
	}
	return render(), budgetOmission(omitted)
}

func budgetOmission(count int) []Omission {
	if count <= 0 {
		return nil
	}
	return []Omission{{Layer: LayerEpisodic, Reason: "budget", Count: count}}
}

func renderSummaryBody(summary StructuredSummary) string {
	var builder strings.Builder
	builder.WriteString("【对话摘要】")
	if summary.Background != "" {
		builder.WriteString("\n背景：")
		builder.WriteString(summary.Background)
	}
	if len(summary.Facts) > 0 {
		builder.WriteString("\n已确认事实：")
		for _, fact := range summary.Facts {
			builder.WriteString("\n- ")
			builder.WriteString(slotLabelOf(fact.Key))
			builder.WriteString("：")
			builder.WriteString(fact.Value)
			if len(fact.Evidence) > 0 {
				builder.WriteString("（第 ")
				builder.WriteString(formatTurns(fact.Evidence))
				builder.WriteString(" 轮）")
			}
		}
	}
	writeList := func(title string, items []string) {
		if len(items) == 0 {
			return
		}
		builder.WriteString("\n")
		builder.WriteString(title)
		for _, item := range items {
			builder.WriteString("\n- ")
			builder.WriteString(item)
		}
	}
	writeList("已做出的决定：", summary.Decisions)
	writeList("未完成事项：", summary.OpenItems)
	writeList("已被否决的方案：", summary.Rejected)
	return builder.String()
}

func formatTurns(evidence []int) string {
	unique := make([]int, 0, len(evidence))
	seen := make(map[int]struct{}, len(evidence))
	for _, turn := range evidence {
		if _, ok := seen[turn]; ok {
			continue
		}
		seen[turn] = struct{}{}
		unique = append(unique, turn)
	}
	sort.Ints(unique)
	if len(unique) > 3 {
		unique = unique[:3]
	}
	parts := make([]string, 0, len(unique))
	for _, turn := range unique {
		parts = append(parts, fmt.Sprintf("%d", turn))
	}
	return strings.Join(parts, "、")
}

// SummaryOrder 摘要状态的单调序：created_at 毫秒 + 消息 ID。
// 只比较 ID 不行——UUID 没有时间顺序；只比较毫秒也不行——同毫秒多条消息。
type SummaryOrder struct {
	CreatedMS int64
	MsgID     string
}

// After 判断 a 是否严格晚于 b。
func (a SummaryOrder) After(b SummaryOrder) bool {
	if a.CreatedMS != b.CreatedMS {
		return a.CreatedMS > b.CreatedMS
	}
	return a.MsgID > b.MsgID
}

// SummaryState 摘要的持久化状态。
type SummaryState struct {
	ConversationID string
	Version        int
	UpToMsgID      string
	Order          SummaryOrder
	Payload        []byte
	UpdatedAt      time.Time
}

// DecodeSummary 把 payload 还原为结构化摘要。
// Version==1 的旧摘要当作纯文本 background，不阻塞升级。
func (s SummaryState) DecodeSummary() StructuredSummary {
	if len(s.Payload) == 0 {
		return StructuredSummary{}
	}
	var summary StructuredSummary
	if err := json.Unmarshal(s.Payload, &summary); err != nil {
		return StructuredSummary{Background: string(s.Payload)}
	}
	summary.normalize()
	return summary
}
