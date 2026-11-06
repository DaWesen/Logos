package ctxmem

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseStructuredSummaryWithCodeFence(t *testing.T) {
	raw := "```json\n" + `{
		"background": "用户在处理退款",
		"facts": [{"key":"order_id","value":"20261106001","evidence":[3]}],
		"decisions": ["选择退款"],
		"open_items": [],
		"rejected": ["上门取件"]
	}` + "\n```"
	summary, err := ParseStructuredSummary(raw)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if summary.Background != "用户在处理退款" || len(summary.Facts) != 1 {
		t.Fatalf("解析结果不正确: %+v", summary)
	}
	if summary.Facts[0].Evidence[0] != 3 {
		t.Fatalf("evidence 丢失: %+v", summary.Facts[0])
	}
}

func TestParseStructuredSummaryRejectsProse(t *testing.T) {
	if _, err := ParseStructuredSummary("这是一段普通摘要"); err == nil {
		t.Fatal("非 JSON 输出应返回错误，交给 L1 降级")
	}
}

func TestValidateFactsDropsHallucination(t *testing.T) {
	facts := []SummaryFact{
		{Key: "order_id", Value: "20261106001", Evidence: []int{3}},
		{Key: "amount", Value: "1820.00", Evidence: []int{12}},
	}
	corpus := "订单号 20261106001，金额 1280.00 元"
	kept, dropped := ValidateFacts(facts, corpus)
	if len(kept) != 1 || kept[0].Value != "20261106001" {
		t.Fatalf("应只保留真实订单号: %+v", kept)
	}
	if len(dropped) != 1 || dropped[0] != "amount" {
		t.Fatalf("编造金额应被记录: %+v", dropped)
	}
}

func TestTrimFactsPrefersRecentAndFrequent(t *testing.T) {
	facts := []SummaryFact{
		{Key: "a", Value: "old", Evidence: []int{2}},
		{Key: "b", Value: "recent", Evidence: []int{80}},
		{Key: "c", Value: "frequent", Evidence: []int{40, 41, 42}},
	}
	trimmed := TrimFacts(facts, 2)
	if len(trimmed) != 2 {
		t.Fatalf("裁剪条数错误: %d", len(trimmed))
	}
	if trimmed[0].Value != "recent" || trimmed[1].Value != "frequent" {
		t.Fatalf("裁剪顺序错误: %+v", trimmed)
	}
}

func TestRenderStructuredDropsRejectedBeforeFacts(t *testing.T) {
	summary := StructuredSummary{
		Background: "用户在处理一笔电商退款，先后确认过订单与金额。",
		Facts:      []SummaryFact{{Key: "order_id", Value: "20261106001", Evidence: []int{3}}},
		Decisions:  []string{"选择退款而非换货"},
		OpenItems:  []string{"等待商家确认收货地址"},
		Rejected:   []string{"上门取件（用户嫌时间不合适）"},
	}
	full, _ := RenderStructured(summary, 10000, DefaultEstimator{})
	for _, want := range []string{"已确认事实", "订单号：20261106001", "已被否决的方案"} {
		if !strings.Contains(full, want) {
			t.Fatalf("完整摘要缺少 %q: %s", want, full)
		}
	}

	tight, omissions := RenderStructured(summary, 100, DefaultEstimator{})
	if strings.Contains(tight, "上门取件") {
		t.Fatalf("预算不足时应先丢 rejected: %s", tight)
	}
	if !strings.Contains(tight, "20261106001") {
		t.Fatalf("facts 不应先于 rejected 被丢弃: %s", tight)
	}
	if len(omissions) == 0 {
		t.Fatal("裁剪必须记录 omission")
	}
}

func TestRenderStructuredIsDeterministic(t *testing.T) {
	summary := StructuredSummary{
		Background: "背景",
		Facts: []SummaryFact{
			{Key: "order_id", Value: "20261106001", Evidence: []int{3, 9}},
			{Key: "amount", Value: "1280.00", Evidence: []int{12}},
		},
		Decisions: []string{"退款"},
	}
	first, _ := RenderStructured(summary, 1000, DefaultEstimator{})
	for i := 0; i < 100; i++ {
		next, _ := RenderStructured(summary, 1000, DefaultEstimator{})
		if next != first {
			t.Fatalf("第 %d 次渲染不一致", i)
		}
	}
}

func TestSummaryOrderUsesTimestampThenID(t *testing.T) {
	later := SummaryOrder{CreatedMS: 200, MsgID: "a"}
	earlier := SummaryOrder{CreatedMS: 100, MsgID: "z"}
	if !later.After(earlier) {
		t.Fatal("时间更晚应胜出")
	}
	sameMS := SummaryOrder{CreatedMS: 200, MsgID: "b"}
	if !sameMS.After(later) {
		t.Fatal("同毫秒时用 ID 做稳定序")
	}
}

func TestDecodeSummarySupportsLegacyText(t *testing.T) {
	state := SummaryState{Version: 1, Payload: []byte("旧的纯文本摘要")}
	summary := state.DecodeSummary()
	if summary.Background != "旧的纯文本摘要" || len(summary.Facts) != 0 {
		t.Fatalf("旧摘要兼容失败: %+v", summary)
	}
}

func TestEncodeRoundTrip(t *testing.T) {
	original := StructuredSummary{
		Background: "背景",
		Facts:      []SummaryFact{{Key: "order_id", Value: "20261106001", Evidence: []int{3}}},
	}
	payload, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	decoded := SummaryState{Version: SummarySchemaVersion, Payload: payload}.DecodeSummary()
	if decoded.Facts[0].Value != "20261106001" {
		t.Fatalf("往返丢失事实: %+v", decoded)
	}
}
