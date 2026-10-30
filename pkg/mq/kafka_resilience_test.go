package mq

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/segmentio/kafka-go"
)

// TestInvokeHandler_RecoversPanic 验证处理器 panic 被归一成错误而不是击穿 goroutine。
func TestInvokeHandler_RecoversPanic(t *testing.T) {
	msg := &Message{Offset: 1}

	err := invokeHandler(func(*Message) error { panic("boom") }, msg)
	if !errors.Is(err, ErrHandlerPanic) {
		t.Fatalf("panic 应归一为 ErrHandlerPanic，实际 %v", err)
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("错误里应保留 panic 内容便于排查，实际 %q", err.Error())
	}

	// 正常返回的错误必须原样透出，不能被 recover 吞掉或改写。
	want := errors.New("db timeout")
	if got := invokeHandler(func(*Message) error { return want }, msg); !errors.Is(got, want) {
		t.Fatalf("普通错误应原样返回，实际 %v", got)
	}

	if err := invokeHandler(func(*Message) error { return nil }, msg); err != nil {
		t.Fatalf("成功处理不应返回错误，实际 %v", err)
	}
}

// TestProcessWithRetry_PanicIsRetriedThenCommitted 验证 panic 走既有的就地重试路径：
// 消费循环不退出、同一条消息重试到成功后才提交，之后的 offset 正常推进。
func TestProcessWithRetry_PanicIsRetriedThenCommitted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	msgA := kafka.Message{Topic: "chat_messages", Offset: 30}
	msgB := kafka.Message{Topic: "chat_messages", Offset: 31}
	src := newFakeSource(msgA, msgB)

	var mu sync.Mutex
	callsA := 0
	handler := func(msg *Message) error {
		if msg.Offset != 30 {
			return nil
		}
		mu.Lock()
		callsA++
		attempt := callsA
		mu.Unlock()
		// 前两次 panic（模拟 handler 内的空指针/db 越界），第三次正常返回。
		if attempt < 3 {
			panic("handler blew up")
		}
		return nil
	}

	c := &Consumer{reader: src, retryBackoff: time.Millisecond}
	if err := c.Subscribe(ctx, handler); err != nil {
		t.Fatalf("Subscribe error: %v", err)
	}

	waitCommits(t, src, 2)

	mu.Lock()
	gotCalls := callsA
	mu.Unlock()
	if gotCalls != 3 {
		t.Fatalf("panic 后应继续重试同一条消息（共 3 次），实际 %d 次", gotCalls)
	}
	if got := src.committedOffsets(); len(got) != 2 || got[0] != 30 || got[1] != 31 {
		t.Fatalf("提交顺序/内容不符（说明消费循环未被打断）: %v", got)
	}
}

// TestProcessWithRetry_PanicAlwaysDeadLettered 验证持续 panic 的毒丸消息：
// 重试耗尽后转存死信并提交跳过，而不是让分区永久卡死。
func TestProcessWithRetry_PanicAlwaysDeadLettered(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	src := newFakeSource(kafka.Message{Topic: "im_messages", Offset: 40})

	var mu sync.Mutex
	calls := 0
	deadLetters := 0
	handler := func(*Message) error {
		mu.Lock()
		calls++
		mu.Unlock()
		panic("always panics")
	}

	c := &Consumer{
		reader:       src,
		retryBackoff: time.Millisecond,
		deadLetter: func(*Message, error) bool {
			mu.Lock()
			deadLetters++
			mu.Unlock()
			return true
		},
	}
	if err := c.Subscribe(ctx, handler); err != nil {
		t.Fatalf("Subscribe error: %v", err)
	}

	waitCommits(t, src, 1)

	mu.Lock()
	gotCalls, gotDL := calls, deadLetters
	mu.Unlock()
	if gotCalls != maxConsumerFailures {
		t.Fatalf("持续 panic 应重试 %d 次，实际 %d 次", maxConsumerFailures, gotCalls)
	}
	if gotDL != 1 {
		t.Fatalf("持续 panic 的消息应转存死信一次，实际 %d 次", gotDL)
	}
	if got := src.committedOffsets(); len(got) != 1 || got[0] != 40 {
		t.Fatalf("毒丸消息应提交跳过，实际提交 %v", got)
	}
}

// TestNewProducer_RetriesAfterCooldown 验证生产者初始化失败不再被 sync.Once 永久钉死：
// 冷却期内不重复连接，冷却结束后自动恢复。
func TestNewProducer_RetriesAfterCooldown(t *testing.T) {
	origInit, origNow, origCooldown := producerInit, producerNow, producerRetryCooldown
	resetProducerState := func() {
		producerMu.Lock()
		producerInstance = nil
		producerInitErr = nil
		producerLastAttempt = time.Time{}
		producerMu.Unlock()
	}
	resetProducerState()
	t.Cleanup(func() {
		producerInit, producerNow, producerRetryCooldown = origInit, origNow, origCooldown
		resetProducerState()
	})

	producerRetryCooldown = 5 * time.Second
	now := time.Unix(0, 0)
	producerNow = func() time.Time { return now }

	calls := 0
	producerInit = func() (*Producer, error) {
		calls++
		return nil, errors.New("broker down")
	}

	if p := NewProducer(); p != nil {
		t.Fatalf("初始化失败时应返回 nil，实际 %v", p)
	}
	if p := NewProducer(); p != nil {
		t.Fatalf("冷却期内仍应返回 nil，实际 %v", p)
	}
	if calls != 1 {
		t.Fatalf("冷却期内不应重新连接 broker，实际初始化 %d 次", calls)
	}

	// 冷却结束：broker 已恢复，下一次调用应真正重建并缓存。
	now = now.Add(6 * time.Second)
	producerInit = func() (*Producer, error) {
		calls++
		return &Producer{}, nil
	}
	p := NewProducer()
	if p == nil {
		t.Fatal("冷却结束后应重新初始化成功")
	}
	if calls != 2 {
		t.Fatalf("冷却结束后应且仅应重试一次，实际初始化 %d 次", calls)
	}
	if again := NewProducer(); again != p {
		t.Fatal("成功后应复用同一实例")
	}
	if calls != 2 {
		t.Fatalf("成功后不应再初始化，实际 %d 次", calls)
	}
}
