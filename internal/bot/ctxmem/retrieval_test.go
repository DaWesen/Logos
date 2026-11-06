package ctxmem

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestDetectTrigger(t *testing.T) {
	slots := []Slot{{Key: "order_id", Value: "20261106001"}}
	cases := []struct {
		name  string
		query string
		slots []Slot
		rule  string
		want  bool
	}{
		{name: "T1 指代且有槽位", query: "那个订单到哪了", slots: slots, rule: "T1", want: true},
		{name: "T1 无槽位不触发", query: "那个订单到哪了", slots: nil, want: false},
		{name: "T2 新单号", query: "查一下 20261106002", slots: slots, rule: "T2", want: true},
		{name: "T2 已知单号不触发", query: "查一下 20261106001", slots: slots, want: false},
		{name: "T3 显式回溯", query: "翻一下前面说的方案", slots: slots, rule: "T3", want: true},
		{name: "普通寒暄不触发", query: "今天天气怎么样", slots: slots, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := DetectTrigger(tc.query, tc.slots)
			if ok != tc.want {
				t.Fatalf("trigger=%v want %v", ok, tc.want)
			}
			if ok && got.Rule != tc.rule {
				t.Fatalf("rule=%s want %s", got.Rule, tc.rule)
			}
		})
	}
}

func TestBigramTermsChinese(t *testing.T) {
	terms := bigramTerms("订单号是 20261106001。")
	joined := strings.Join(terms, ",")
	for _, want := range []string{"订单", "单号", "20", "00", "01"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("缺少 bigram %q: %s", want, joined)
		}
	}
	if strings.Contains(joined, "。") || strings.Contains(joined, " 2") {
		t.Fatalf("标点/空白不应进入 token: %s", joined)
	}
}

func TestSearchFindsOrderFromTurnThree(t *testing.T) {
	messages := make([]Message, 0, 600)
	for i := 1; i <= 200; i++ {
		content := fmt.Sprintf("第 %d 轮普通对话", i)
		if i == 3 {
			content = "订单号 20261106001，麻烦帮我查下物流。"
		}
		messages = append(messages,
			Message{ID: fmt.Sprintf("u-%d", i), Role: "user", Content: content, TurnIndex: i},
			Message{ID: fmt.Sprintf("a-%d", i), Role: "assistant", Content: "收到", TurnIndex: i},
		)
	}
	index := BuildIndex(messages)
	slots := []Slot{{Key: "order_id", Value: "20261106001"}}

	hits := index.Search("那个订单到哪了", slots)
	if len(hits) == 0 {
		t.Fatal("没有召回任何消息")
	}
	found := false
	for _, hit := range hits {
		if strings.Contains(hit.Doc.Content, "20261106001") {
			found = true
			if hit.Doc.TurnIndex != 3 {
				t.Fatalf("出处轮次错误: %d", hit.Doc.TurnIndex)
			}
		}
	}
	if !found {
		t.Fatalf("第 3 轮的订单号未被召回: %+v", hits)
	}
}

func TestSlotExactMatchOutranksKeywordOnly(t *testing.T) {
	messages := []Message{
		{ID: "m1", Role: "user", Content: "我想咨询一下订单相关的问题", TurnIndex: 1},
		{ID: "m2", Role: "user", Content: "订单号 20261106001，地址是杭州", TurnIndex: 2},
	}
	index := BuildIndex(messages)
	slots := []Slot{{Key: "order_id", Value: "20261106001"}}
	hits := index.Search("那个订单", slots)
	if len(hits) < 2 {
		t.Fatalf("期望召回两条，实际 %d", len(hits))
	}
	if hits[0].Doc.MsgID != "m2" {
		t.Fatalf("精确匹配应排第一，实际 %s (%s)", hits[0].Doc.MsgID, hits[0].Route)
	}
}

func TestRenderHitsWithSourceAndBudget(t *testing.T) {
	hits := []Hit{
		{Doc: &Doc{MsgID: "m1", TurnIndex: 3, Role: "user", Content: "订单号 20261106001，麻烦帮我查下物流。"}},
		{Doc: &Doc{MsgID: "m2", TurnIndex: 41, Role: "user", Content: "刚才说错了，是 20261106001 不是 002。"}},
		{Doc: &Doc{MsgID: "m3", TurnIndex: 42, Role: "assistant", Content: "已为您核实。"}},
	}
	text, omissions := RenderHits(hits, 2000, map[string]struct{}{"m2": {}}, DefaultEstimator{})
	if !strings.Contains(text, "[第 3 轮 · 用户]") {
		t.Fatalf("缺少出处标注: %s", text)
	}
	if strings.Contains(text, "m2") || strings.Contains(text, "刚才说错了") {
		t.Fatalf("工作层已包含的消息不应重复注入: %s", text)
	}
	if !strings.Contains(text, "裁决规则") {
		t.Fatalf("注入必须带冲突裁决规则: %s", text)
	}
	for _, omission := range omissions {
		if omission.Layer != LayerRetrieval {
			t.Fatalf("omission layer=%v", omission.Layer)
		}
	}

	text, omissions = RenderHits(hits, 10, nil, DefaultEstimator{})
	if text != "" {
		t.Fatalf("预算不足时应放弃注入: %s", text)
	}
	if len(omissions) == 0 || omissions[0].Reason != "budget" {
		t.Fatalf("应记录 budget 降级: %+v", omissions)
	}
}

func TestIndexCacheReuseAndFingerprint(t *testing.T) {
	now := time.Unix(1000, 0)
	cache := NewIndexCache(time.Minute, 2)
	cache.clock = func() time.Time { return now }

	index := BuildIndex([]Message{{ID: "m1", Role: "user", Content: "订单 20261106001"}})
	cache.Put("conv", "1:m1", index)
	if got := cache.Get("conv", "1:m1"); got != index {
		t.Fatal("相同指纹应命中缓存")
	}
	if got := cache.Get("conv", "2:m2"); got != nil {
		t.Fatal("会话有新消息时必须重建")
	}

	now = now.Add(2 * time.Minute)
	if got := cache.Get("conv", "1:m1"); got != nil {
		t.Fatal("过期索引不应继续使用")
	}
}

func TestFingerprintChangesWithNewMessage(t *testing.T) {
	base := []Message{{ID: "m1"}, {ID: "m2"}}
	next := append([]Message(nil), base...)
	next = append(next, Message{ID: "m3"})
	if Fingerprint(base) == Fingerprint(next) {
		t.Fatal("追加消息后指纹必须变化")
	}
}
