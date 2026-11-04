package qqbridge

import (
	"context"
	"sync"
	"testing"
	"time"

	"Logos/internal/service/messaging/types"
	"Logos/pkg/client"
)

func TestCloneEvent_DeepCopiesMutableFields(t *testing.T) {
	original := &types.MessageEvent{
		ChatID:         "chat-1",
		Metadata:       map[string]string{"source": "qq"},
		MentionUserIDs: []string{"qq_1"},
		MediaMeta:      []byte(`{"size":1}`),
		Extra: map[string]interface{}{
			"model_config": map[string]interface{}{"model": "v1"},
			"tags":         []interface{}{"a", "b"},
		},
	}
	cloned := cloneEvent(original)

	original.Metadata["source"] = "changed"
	original.MentionUserIDs[0] = "changed"
	original.MediaMeta[0] = 'X'
	original.Extra["model_config"].(map[string]interface{})["model"] = "changed"
	original.Extra["tags"].([]interface{})[0] = "changed"

	if cloned.Metadata["source"] != "qq" {
		t.Fatal("Metadata 未深拷贝")
	}
	if cloned.MentionUserIDs[0] != "qq_1" {
		t.Fatal("MentionUserIDs 未深拷贝")
	}
	if cloned.MediaMeta[0] != '{' {
		t.Fatal("MediaMeta 未深拷贝")
	}
	if cloned.Extra["model_config"].(map[string]interface{})["model"] != "v1" {
		t.Fatal("Extra 嵌套 map 未深拷贝")
	}
	if cloned.Extra["tags"].([]interface{})[0] != "a" {
		t.Fatal("Extra 嵌套 slice 未深拷贝")
	}
}

func TestReserveFastReply_DropsWhenSlotsFull(t *testing.T) {
	bridge := &Bridge{
		fastSlots: make(chan struct{}, 1),
		fastReply: func(*types.MessageEvent, *InboundMessage) fastReplyOutcome { return fastReplyOK },
	}
	bridge.fastSlots <- struct{}{} // 占满
	defer func() { <-bridge.fastSlots }()

	start := time.Now()
	reserved := bridge.reserveFastReply(context.Background())
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("槽位满时不应阻塞，耗时 %v", elapsed)
	}
	if reserved {
		t.Fatal("槽位满时不应预留成功")
	}
}

func TestCommitFastReply_ReleasesSlotAfterWork(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	bridge := &Bridge{
		fastSlots: make(chan struct{}, 1),
		fastReply: func(*types.MessageEvent, *InboundMessage) fastReplyOutcome {
			wg.Done()
			return fastReplyOK
		},
	}

	if !bridge.reserveFastReply(context.Background()) {
		t.Fatal("有空闲槽位时应预留成功")
	}
	bridge.commitFastReply(
		&types.MessageEvent{ChatID: "chat-1"},
		&InboundMessage{MessageType: "private"},
	)
	wg.Wait()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(bridge.fastSlots) == 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("快速路径槽位未释放: %d", len(bridge.fastSlots))
}

func TestCommitFastReply_PanicDoesNotEscapeOrLeakSlot(t *testing.T) {
	bridge := &Bridge{
		fastSlots: make(chan struct{}, 1),
		fastReply: func(*types.MessageEvent, *InboundMessage) fastReplyOutcome { panic("boom") },
	}

	if !bridge.reserveFastReply(context.Background()) {
		t.Fatal("有空闲槽位时应预留成功")
	}
	bridge.commitFastReply(
		&types.MessageEvent{ChatID: "chat-1"},
		&InboundMessage{MessageType: "private"},
	)

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(bridge.fastSlots) == 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("panic 后槽位未释放: %d", len(bridge.fastSlots))
}

// TestCommitFastReply_ReleasesSlotBeforeRecovery
// 补偿要发 Kafka，最长 5s；如果还握着槽位，Kafka 抖动会把快速路径容量
// 一起拖住。这里验证「槽位先归还」的顺序：补偿期间槽位必须是空的。
func TestCommitFastReply_ReleasesSlotBeforeRecovery(t *testing.T) {
	slotReleased := make(chan struct{})
	recoveryCalled := make(chan struct{})
	var bridge *Bridge
	bridge = &Bridge{
		fastSlots: make(chan struct{}, 1),
		fastReply: func(*types.MessageEvent, *InboundMessage) fastReplyOutcome {
			close(slotReleased)
			return fastReplyBotFailed
		},
		publishRecovery: func(*types.MessageEvent, fastReplyOutcome) {
			// 补偿发布发生在槽位归还之后：否则 Kafka 抖动会把并发容量拖住。
			if len(bridge.fastSlots) != 0 {
				t.Errorf("发布补偿时槽位应已归还，当前占用 %d", len(bridge.fastSlots))
			}
			close(recoveryCalled)
		},
	}

	if !bridge.reserveFastReply(context.Background()) {
		t.Fatal("有空闲槽位时应预留成功")
	}
	bridge.commitFastReply(
		&types.MessageEvent{ChatID: "chat-1", Metadata: map[string]string{}},
		&InboundMessage{MessageType: "private"},
	)

	select {
	case <-slotReleased:
	case <-time.After(2 * time.Second):
		t.Fatal("快速路径没有执行")
	}
	select {
	case <-recoveryCalled:
	case <-time.After(2 * time.Second):
		t.Fatal("可补偿失败应发布补偿事件")
	}
}

func TestReleaseFastSlot_ReturnsReservedSlot(t *testing.T) {
	bridge := &Bridge{
		fastSlots: make(chan struct{}, 1),
	}
	if !bridge.reserveFastReply(context.Background()) {
		t.Fatal("有空闲槽位时应预留成功")
	}
	bridge.releaseFastSlot()
	if len(bridge.fastSlots) != 0 {
		t.Fatalf("发布失败后槽位应释放，当前占用 %d", len(bridge.fastSlots))
	}

	// 幂等：重复释放不能 panic，也不能变成负数/阻塞。
	bridge.releaseFastSlot()
	if len(bridge.fastSlots) != 0 {
		t.Fatalf("重复释放不应改变槽位占用，当前 %d", len(bridge.fastSlots))
	}
}

func TestReleaseFastSlot_NilChannelIsNoop(t *testing.T) {
	bridge := &Bridge{}
	bridge.releaseFastSlot()
}

func TestCanDispatchFastReply_RequiresEligibleMessage(t *testing.T) {
	bridge := &Bridge{
		fastSlots: make(chan struct{}, 1),
		fastReply: func(*types.MessageEvent, *InboundMessage) fastReplyOutcome { return fastReplyOK },
	}
	// botClient 为 nil：快速路径不可用，必须返回 false 让 Chat Service 兜底。
	if bridge.canDispatchFastReply(&InboundMessage{MessageType: "private"}) {
		t.Fatal("Bot 客户端不可用时不应接管快速路径")
	}
	if len(bridge.fastSlots) != 0 {
		t.Fatal("未接管时不应占用槽位")
	}

	bridge.botClient = &client.BotClient{}
	if !bridge.canDispatchFastReply(&InboundMessage{MessageType: "private"}) {
		t.Fatal("Bot 客户端可用时私聊消息应接管快速路径")
	}

	for _, msg := range []*InboundMessage{
		nil,
		{MessageType: "private", IsSent: true},
		{MessageType: "group"},
	} {
		if bridge.canDispatchFastReply(msg) {
			t.Fatalf("消息 %+v 不应接管快速路径", msg)
		}
	}
}

func TestFastReplyOutcome_CompensableClassification(t *testing.T) {
	// 只有「再试一次可能成功」的失败才值得补偿。
	for _, outcome := range []fastReplyOutcome{
		fastReplyPanic,
		fastReplyBotFailed,
		fastReplyEmptyContent,
		fastReplyUndeliverable,
	} {
		if !outcome.compensable() {
			t.Fatalf("%q 应可补偿", outcome)
		}
	}

	for _, outcome := range []fastReplyOutcome{fastReplyOK, fastReplyNoBinding} {
		if outcome.compensable() {
			t.Fatalf("%q 不应补偿", outcome)
		}
	}
}

func TestBuildFastRecoveryEvent_ClearsDirectBotContract(t *testing.T) {
	original := &types.MessageEvent{
		ID:             "msg-1",
		EventType:      types.EventTypeMessage,
		ChatID:         "bot-b1",
		ChatType:       types.ChatTypePrivate,
		SenderID:       "u1",
		Content:        "hello",
		MentionUserIDs: []string{"bot_b1"},
		Metadata: map[string]string{
			types.MetaQQDirectBot: "true",
			types.MetaQQSelfID:    "20002",
			types.MetaQQUserID:    "10001",
			"source":              "qq",
		},
	}

	recovery := buildFastRecoveryEvent(original, fastReplyBotFailed)
	if recovery == nil {
		t.Fatal("可补偿失败应生成补偿事件")
	}
	if recovery.ID == original.ID {
		t.Fatal("补偿事件必须使用新的消息 ID，否则消费端幂等会把它当成重复消息")
	}
	if recovery.EventType != types.EventTypeBotRecovery {
		t.Fatalf("补偿事件类型错误: %s", recovery.EventType)
	}
	if _, ok := recovery.Metadata[types.MetaQQDirectBot]; ok {
		t.Fatal("补偿事件必须撤销 qq_direct_bot，否则慢路径仍会跳过")
	}
	if recovery.Metadata[types.MetaQQFastRecovery] != "true" {
		t.Fatal("补偿事件缺少 qq_fast_recovery 标记")
	}
	if recovery.Metadata[types.MetaQQFastReason] != string(fastReplyBotFailed) {
		t.Fatalf("补偿原因错误: %s", recovery.Metadata[types.MetaQQFastReason])
	}
	if recovery.Metadata[types.MetaQQUserID] != "10001" || recovery.Metadata[types.MetaQQSelfID] != "20002" {
		t.Fatal("补偿事件必须保留出站发送所需的 QQ 地址")
	}
	if recovery.RecipientIDs != nil {
		t.Fatal("补偿事件不应携带接收者列表，避免被当成普通消息推送")
	}
	if recovery.MentionUserIDs[0] != "bot_b1" {
		t.Fatal("补偿事件必须保留 Bot 提及，慢路径才知道要调哪个 Bot")
	}

	// 原事件不能被修改：补偿是复制出来的独立事件。
	if original.Metadata[types.MetaQQDirectBot] != "true" {
		t.Fatal("构造补偿事件不应修改原事件")
	}
}

func TestBuildFastRecoveryEvent_IgnoresNonCompensable(t *testing.T) {
	event := &types.MessageEvent{ID: "msg-1", ChatID: "bot-b1"}

	if got := buildFastRecoveryEvent(nil, fastReplyBotFailed); got != nil {
		t.Fatal("nil 事件不应生成补偿")
	}
	if got := buildFastRecoveryEvent(event, fastReplyOK); got != nil {
		t.Fatal("成功结果不应生成补偿")
	}
	if got := buildFastRecoveryEvent(event, fastReplyNoBinding); got != nil {
		t.Fatal("未绑定 Bot 不应补偿：重试也不会成功")
	}
}
