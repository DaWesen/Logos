package mq

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"testing"
	"time"

	"Logos/pkg/logger"

	"github.com/segmentio/kafka-go"
	"go.uber.org/zap"
)

// TestMain 注入 nop logger，避免测试依赖配置文件
func TestMain(m *testing.M) {
	logger.SetLogger(zap.NewNop())
	os.Exit(m.Run())
}

// fakeSource 模拟 kafka.Reader 的拉取/提交语义：游标只前进不回退，
// 未提交的消息不会在本进程内被再次返回（这正是原先缺陷的根源）。
type fakeSource struct {
	mu       sync.Mutex
	msgs     []kafka.Message
	fetched  int
	commits  []kafka.Message
	events   []string // 有序记录 fetch/commit，用于断言「提交前不拉取下一条」
	commitCh chan kafka.Message
	closed   bool
}

func newFakeSource(msgs ...kafka.Message) *fakeSource {
	return &fakeSource{msgs: msgs, commitCh: make(chan kafka.Message, 16)}
}

func (f *fakeSource) FetchMessage(ctx context.Context) (kafka.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fetched >= len(f.msgs) {
		return kafka.Message{}, io.EOF
	}
	msg := f.msgs[f.fetched]
	f.fetched++
	f.events = append(f.events, fmt.Sprintf("fetch:%d", msg.Offset))
	return msg, nil
}

func (f *fakeSource) CommitMessages(_ context.Context, msgs ...kafka.Message) error {
	f.mu.Lock()
	f.commits = append(f.commits, msgs...)
	for _, m := range msgs {
		f.events = append(f.events, fmt.Sprintf("commit:%d", m.Offset))
	}
	f.mu.Unlock()
	for _, m := range msgs {
		f.commitCh <- m
	}
	return nil
}

func (f *fakeSource) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func (f *fakeSource) committedOffsets() []int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]int64, 0, len(f.commits))
	for _, m := range f.commits {
		out = append(out, m.Offset)
	}
	return out
}

func (f *fakeSource) eventLog() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.events...)
}

func (f *fakeSource) fetchedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.fetched
}

func waitCommits(t *testing.T, src *fakeSource, want int) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for len(src.committedOffsets()) < want {
		select {
		case <-src.commitCh:
		case <-deadline:
			t.Fatalf("等待 %d 次提交超时，实际提交: %v", want, src.committedOffsets())
		}
	}
}

// TestProcessWithRetry_RetriesInPlaceThenCommits 验证核心语义：
// 处理器瞬时失败时对**同一条**消息就地重试，成功后才提交，且不会提前消费下一条。
func TestProcessWithRetry_RetriesInPlaceThenCommits(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	msgA := kafka.Message{Topic: "chat_messages", Partition: 0, Offset: 10}
	msgB := kafka.Message{Topic: "chat_messages", Partition: 0, Offset: 11}
	src := newFakeSource(msgA, msgB)

	var mu sync.Mutex
	calls := map[int64]int{}
	handler := func(msg *Message) error {
		mu.Lock()
		calls[msg.Offset]++
		attempt := calls[msg.Offset]
		mu.Unlock()
		// A 的前两次失败，第三次成功；B 一次成功
		if msg.Offset == 10 && attempt < 3 {
			return fmt.Errorf("db timeout (attempt %d)", attempt)
		}
		return nil
	}

	c := &Consumer{reader: src, retryBackoff: time.Millisecond}
	if err := c.Subscribe(ctx, handler); err != nil {
		t.Fatalf("Subscribe error: %v", err)
	}

	waitCommits(t, src, 2)

	mu.Lock()
	aCalls := calls[10]
	mu.Unlock()
	if aCalls != 3 {
		t.Fatalf("消息 A 应被就地重试至成功（共 3 次），实际 %d 次", aCalls)
	}
	if got := src.committedOffsets(); len(got) != 2 || got[0] != 10 || got[1] != 11 {
		t.Fatalf("提交顺序/内容不符: %v（A 必须先成功提交后才会消费 B）", got)
	}
}

// TestProcessWithRetry_PoisonMessageDeadLettered 验证毒丸处理：
// 连续失败达上限后转存死信并提交跳过；期间不拉取下一条消息，避免越界丢消息。
func TestProcessWithRetry_PoisonMessageDeadLettered(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	msgA := kafka.Message{Topic: "im_messages", Partition: 3, Offset: 7, Key: []byte("u1"), Value: []byte(`{"bad":true}`)}
	msgB := kafka.Message{Topic: "im_messages", Partition: 3, Offset: 8}
	src := newFakeSource(msgA, msgB)

	var mu sync.Mutex
	callsByOffset := map[int64]int{}
	deadLetters := 0
	var deadLetterMsg *Message
	handler := func(msg *Message) error {
		mu.Lock()
		callsByOffset[msg.Offset]++
		mu.Unlock()
		return errors.New("always fail")
	}

	c := &Consumer{
		reader:       src,
		retryBackoff: time.Millisecond,
		deadLetter: func(msg *Message, _ error) bool {
			mu.Lock()
			deadLetters++
			deadLetterMsg = msg
			mu.Unlock()
			return true
		},
	}
	if err := c.Subscribe(ctx, handler); err != nil {
		t.Fatalf("Subscribe error: %v", err)
	}

	waitCommits(t, src, 1)

	mu.Lock()
	calls, dlCount, dlMsg := callsByOffset[7], deadLetters, deadLetterMsg
	mu.Unlock()

	if calls != maxConsumerFailures {
		t.Fatalf("同一条消息应重试 %d 次，实际 %d 次", maxConsumerFailures, calls)
	}
	if dlCount != 1 || dlMsg == nil || dlMsg.Offset != 7 {
		t.Fatalf("毒丸消息应转存死信一次且携带原始消息，实际 %d 次，消息 %+v", dlCount, dlMsg)
	}
	if got := src.committedOffsets(); len(got) < 1 || got[0] != 7 {
		t.Fatalf("毒丸处理完应先提交该条 offset，实际 %v", got)
	}
	// 关键顺序：先拉 7、重试耗尽后才提交 7，之后才允许拉 8。
	// 若像旧实现那样「失败即继续拉下一条」，后续 commit 会把 7 永久越过（丢消息）。
	logs := src.eventLog()
	if len(logs) < 2 || logs[0] != "fetch:7" || logs[1] != "commit:7" {
		t.Fatalf("事件顺序应为 fetch:7 → commit:7，实际 %v", logs)
	}
	for i, ev := range logs {
		if ev != "fetch:8" {
			continue
		}
		committedFirst := false
		for _, before := range logs[:i] {
			if before == "commit:7" {
				committedFirst = true
				break
			}
		}
		if !committedFirst {
			t.Fatalf("offset 8 必须在 offset 7 提交之后才拉取，实际事件 %v", logs)
		}
	}
}

// TestProcessWithRetry_CancelDuringRetryLeavesUncommitted 验证优雅退出：
// ctx 取消后立即停止重试，且不提交 offset —— 重启后会从已提交位点重投，不丢消息。
func TestProcessWithRetry_CancelDuringRetryLeavesUncommitted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	src := newFakeSource(kafka.Message{Topic: "chat_messages", Offset: 20})

	var mu sync.Mutex
	calls := 0
	handler := func(*Message) error {
		mu.Lock()
		calls++
		mu.Unlock()
		cancel() // 第一次处理即触发取消（模拟服务优雅停机）
		return errors.New("shutting down")
	}

	c := &Consumer{reader: src, retryBackoff: time.Millisecond}
	if err := c.Subscribe(ctx, handler); err != nil {
		t.Fatalf("Subscribe error: %v", err)
	}

	deadline := time.After(2 * time.Second)
	for {
		src.mu.Lock()
		closed := src.closed
		src.mu.Unlock()
		if closed {
			break
		}
		select {
		case <-deadline:
			t.Fatal("ctx 取消后消费循环未退出")
		case <-time.After(5 * time.Millisecond):
		}
	}

	mu.Lock()
	gotCalls := calls
	mu.Unlock()
	if gotCalls != 1 {
		t.Fatalf("取消后不应继续重试，处理器调用 %d 次", gotCalls)
	}
	if got := src.committedOffsets(); len(got) != 0 {
		t.Fatalf("取消时不得提交 offset，实际提交 %v", got)
	}
}

// TestIsExpectedFetchError 验证用 errors.Is 判类（而非字符串匹配）的降噪逻辑。
func TestIsExpectedFetchError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"io.EOF", io.EOF, true},
		{"包装后的 EOF", fmt.Errorf("读取消息失败: %w", io.EOF), true},
		{"ctx 取消", context.Canceled, true},
		{"业务错误", errors.New("connection reset by peer"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isExpectedFetchError(tc.err); got != tc.want {
				t.Fatalf("isExpectedFetchError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
