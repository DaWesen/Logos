package memory

import (
	"testing"
	"time"

	botmodel "Logos/internal/service/ai/bot/model"
)

func msgAt(content string, at time.Time) *botmodel.Message {
	return &botmodel.Message{Content: content, CreatedAt: at}
}

// TestDecideExtraction_FirstRunNeedsTwoMessages 单条消息不足以支撑任何事实。
func TestDecideExtraction_FirstRunNeedsTwoMessages(t *testing.T) {
	now := time.Now()
	if _, run, _ := decideExtraction(nil, []*botmodel.Message{msgAt("hi", now)}, now); run {
		t.Fatal("单条消息不应触发提取")
	}

	msgs := []*botmodel.Message{msgAt("hi", now.Add(-time.Minute)), msgAt("hello", now)}
	if _, run, next := decideExtraction(nil, msgs, now); !run || next == nil {
		t.Fatal("两条消息应触发首次提取")
	}
}

// TestDecideExtraction_NoNewMessagesSkips 同一批消息重复到达不应再次调用 LLM。
func TestDecideExtraction_NoNewMessagesSkips(t *testing.T) {
	now := time.Now()
	prev := &extractionState{lastRun: now, lastMsg: now}
	msgs := []*botmodel.Message{msgAt("a", now.Add(-time.Minute)), msgAt("b", now)}

	batch, run, next := decideExtraction(prev, msgs, now.Add(time.Minute))
	if run {
		t.Fatal("没有新消息不应触发")
	}
	if batch != nil || next != prev {
		t.Fatal("跳过时不应改变状态")
	}
}

// TestDecideExtraction_AccumulatedMessages 攒够阈值后只喂增量消息。
func TestDecideExtraction_AccumulatedMessages(t *testing.T) {
	base := time.Now().Add(-10 * time.Minute)
	prev := &extractionState{lastRun: base, lastMsg: base}
	now := base.Add(10 * time.Minute)
	msgs := []*botmodel.Message{
		msgAt("old", base),
		msgAt("n1", base.Add(time.Minute)),
		msgAt("n2", base.Add(2*time.Minute)),
		msgAt("n3", base.Add(3*time.Minute)),
	}

	batch, run, _ := decideExtraction(prev, msgs, now)
	if !run {
		t.Fatal("三条新消息应触发提取")
	}
	if len(batch) != 3 {
		t.Fatalf("只应喂增量消息，got %d", len(batch))
	}
}

// TestDecideExtraction_ForceAfterMaxAge 增量不足但间隔很久时兜底触发，
// 避免低频用户永远攒不满阈值。
func TestDecideExtraction_ForceAfterMaxAge(t *testing.T) {
	base := time.Now().Add(-2 * time.Hour)
	prev := &extractionState{lastRun: base, lastMsg: base}
	msgs := []*botmodel.Message{
		msgAt("old", base),
		msgAt("new", base.Add(time.Hour)),
	}

	batch, run, _ := decideExtraction(prev, msgs, time.Now())
	if !run || len(batch) != 1 {
		t.Fatalf("超过最长间隔应强制提取一条增量，run=%v len=%d", run, len(batch))
	}
}

// TestDecideExtraction_SkipsWhenBelowThreshold 增量不足且间隔不长时应继续等待。
func TestDecideExtraction_SkipsWhenBelowThreshold(t *testing.T) {
	base := time.Now().Add(-5 * time.Minute)
	prev := &extractionState{lastRun: base, lastMsg: base}
	msgs := []*botmodel.Message{
		msgAt("old", base),
		msgAt("new", base.Add(time.Minute)),
	}

	if _, run, _ := decideExtraction(prev, msgs, time.Now()); run {
		t.Fatal("一条新消息、间隔 5 分钟不应触发")
	}
}

// TestDecideExtraction_NilMessagesSkipped nil 元素不参与时间窗口。
func TestDecideExtraction_NilMessagesSkipped(t *testing.T) {
	base := time.Now().Add(-time.Hour)
	prev := &extractionState{lastRun: base, lastMsg: base}
	msgs := []*botmodel.Message{nil, msgAt("new", base.Add(time.Minute))}

	if _, run, _ := decideExtraction(prev, msgs, time.Now()); !run {
		t.Fatal("nil 之外的增量应触发")
	}
}
