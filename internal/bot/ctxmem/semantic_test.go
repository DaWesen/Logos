package ctxmem

import (
	"strings"
	"testing"
)

func TestExtractSlots_CoversRuleKinds(t *testing.T) {
	content := "订单号是 20240517001，电话 13800138000，邮箱 user@example.com，" +
		"金额 ¥1,299.00，日期 2026-11-20，链接 https://example.com/order/1"
	slots := ExtractSlots(SourceMessage{
		ID:        "msg-1",
		Role:      "user",
		Content:   content,
		TurnIndex: 3,
	})

	wantKeys := []string{"order_id", "phone", "email", "amount", "date", "url"}
	found := make(map[string]Slot, len(slots))
	for _, slot := range slots {
		found[slot.Key] = slot
		if !strings.Contains(content, slot.Value) {
			t.Fatalf("slot %s value %q is not a substring of source", slot.Key, slot.Value)
		}
		if slot.SourceMsgID != "msg-1" || slot.SourceRole != "user" || slot.TurnIndex != 3 {
			t.Fatalf("slot %s lost source metadata: %+v", slot.Key, slot)
		}
	}
	for _, key := range wantKeys {
		if _, ok := found[key]; !ok {
			t.Fatalf("key %q not extracted, got %+v", key, slots)
		}
	}
}

func TestResolveSlot_LaterUserTurnSupersedesAndRendersNote(t *testing.T) {
	oldSlot := Slot{
		ID:          "slot-old",
		Key:         "order_id",
		Value:       "20240517001",
		Kind:        "id",
		SourceRole:  "user",
		SourceMsgID: "msg-3",
		TurnIndex:   3,
	}
	newSlot := Slot{
		ID:          "slot-new",
		Key:         "order_id",
		Value:       "20240517999",
		Kind:        "id",
		SourceRole:  "user",
		SourceMsgID: "msg-40",
		TurnIndex:   40,
	}

	superseded, skip := ResolveSlot([]Slot{oldSlot}, newSlot)
	if skip {
		t.Fatal("new value must not be skipped")
	}
	if len(superseded) != 1 || superseded[0] != oldSlot.ID {
		t.Fatalf("superseded = %v, want [%s]", superseded, oldSlot.ID)
	}

	oldSlot.SupersededBy = newSlot.ID
	rendered, omissions := RenderSlots([]Slot{oldSlot, newSlot}, 4096, nil)
	if len(omissions) != 0 {
		t.Fatalf("unexpected omissions: %+v", omissions)
	}
	for _, want := range []string{
		oldSlot.Value,
		newSlot.Value,
		"曾把",
		"更正为",
		"第 3 轮",
		"第 40 轮",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered slots missing %q:\n%s", want, rendered)
		}
	}
}

func TestResolveSlot_CrossSourceCoexists(t *testing.T) {
	userSlot := Slot{
		ID:         "slot-user",
		Key:        "order_id",
		Value:      "20240517001",
		SourceRole: "user",
		TurnIndex:  3,
	}
	toolSlot := Slot{
		ID:         "slot-tool",
		Key:        "order_id",
		Value:      "20240517999",
		SourceRole: "tool",
		TurnIndex:  4,
	}

	superseded, skip := ResolveSlot([]Slot{userSlot}, toolSlot)
	if skip || len(superseded) != 0 {
		t.Fatalf("cross-source slots must coexist, superseded=%v skip=%v", superseded, skip)
	}

	rendered, _ := RenderSlots([]Slot{userSlot, toolSlot}, 4096, nil)
	if !strings.Contains(rendered, "用户提供") || !strings.Contains(rendered, "工具查询结果") {
		t.Fatalf("rendered slots should label both sources:\n%s", rendered)
	}
}

func TestResolveSlot_SameValueIsIdempotent(t *testing.T) {
	current := Slot{ID: "slot-1", Key: "phone", Value: "13800138000", SourceRole: "user", TurnIndex: 2}
	incoming := current
	incoming.ID = "slot-2"
	incoming.TurnIndex = 5

	superseded, skip := ResolveSlot([]Slot{current}, incoming)
	if !skip || len(superseded) != 0 {
		t.Fatalf("duplicate value should be skipped, superseded=%v skip=%v", superseded, skip)
	}
}

func TestRenderSlots_Deterministic(t *testing.T) {
	slots := ExtractSlots(SourceMessage{
		ID:        "msg-1",
		Role:      "user",
		Content:   "订单号是 20240517001，电话 13800138000，金额 ¥1,299.00，日期 2026-11-20",
		TurnIndex: 1,
	})
	if len(slots) < 4 {
		t.Fatalf("expected at least 4 slots, got %d", len(slots))
	}

	first, _ := RenderSlots(slots, 4096, nil)
	if first == "" {
		t.Fatal("empty render")
	}
	for i := 0; i < 100; i++ {
		got, _ := RenderSlots(slots, 4096, nil)
		if got != first {
			t.Fatalf("render %d differs:\nfirst=%s\ngot=%s", i, first, got)
		}
	}
}

func TestRenderSlots_TrimsByPriorityAndReportsOmission(t *testing.T) {
	slots := []Slot{
		{ID: "slot-url", Key: "url", Kind: "url", Value: "https://example.com/1", SourceRole: "user", TurnIndex: 1},
		{ID: "slot-date", Key: "date", Kind: "date", Value: "2026-11-20", SourceRole: "user", TurnIndex: 1},
		{ID: "slot-amount", Key: "amount", Kind: "amount", Value: "¥1,299.00", SourceRole: "user", TurnIndex: 1},
		{ID: "slot-email", Key: "email", Kind: "id", Value: "user@example.com", SourceRole: "user", TurnIndex: 1},
		{ID: "slot-phone", Key: "phone", Kind: "id", Value: "13800138000", SourceRole: "user", TurnIndex: 1},
		{ID: "slot-order", Key: "order_id", Kind: "id", Value: "20240517001", SourceRole: "user", TurnIndex: 1},
	}

	full, _ := RenderSlots(slots, 4096, nil)
	lines := strings.Split(full, "\n")
	if len(lines) < 3 {
		t.Fatalf("unexpected full render:\n%s", full)
	}
	// 预算刚好容纳标题与最高优先级的订单号，其余事实必须被裁掉。
	budget := EstimateText(strings.Join(lines[:2], "\n"))
	rendered, omissions := RenderSlots(slots, budget, nil)
	if !strings.Contains(rendered, "20240517001") {
		t.Fatalf("highest-priority slot should survive:\n%s", rendered)
	}
	for _, omitted := range []string{"13800138000", "user@example.com", "¥1,299.00", "2026-11-20", "https://example.com/1"} {
		if strings.Contains(rendered, omitted) {
			t.Fatalf("slot %q should be trimmed at budget %d:\n%s", omitted, budget, rendered)
		}
	}
	if len(omissions) != 1 || omissions[0].Layer != LayerSemantic || omissions[0].Count == 0 {
		t.Fatalf("omissions = %+v, want semantic budget omission", omissions)
	}
}
