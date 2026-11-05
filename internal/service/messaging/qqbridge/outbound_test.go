package qqbridge

import (
	"context"
	"errors"
	"testing"

	"Logos/config"
	"Logos/internal/service/messaging/types"
	"Logos/pkg/mq"
)

func newTestBridge() *Bridge {
	cfg := &config.Config{}
	b := &Bridge{
		cfg:        cfg,
		converter:  NewMessageConverter(cfg),
		fastSlots:  make(chan struct{}, 4),
		mediaRelay: NewMediaRelay(cfg),
	}
	b.plugin = NewZeroBotPlugin(b, cfg)
	b.fastReply = b.fastBotReply
	return b
}

func TestHandleOutboundMessage_MissingTargetIsSkippable(t *testing.T) {
	b := newTestBridge()
	err := b.handleOutboundMessage(context.Background(), &types.MessageEvent{
		ChatID:   "chat-1",
		ChatType: types.ChatTypePrivate,
		Content:  "hello",
	})
	if !errors.Is(err, mq.ErrSkipMessage) {
		t.Fatalf("缺少目标 QQ 号应标记为不可恢复，got %v", err)
	}
}

func TestHandleOutboundMessage_MissingSelfIDIsSkippable(t *testing.T) {
	b := newTestBridge()
	err := b.handleOutboundMessage(context.Background(), &types.MessageEvent{
		ChatID:   "chat-1",
		ChatType: types.ChatTypePrivate,
		Content:  "hello",
		Metadata: map[string]string{types.MetaQQUserID: "10001"},
	})
	if !errors.Is(err, mq.ErrSkipMessage) {
		t.Fatalf("缺少 Bot QQ 号应标记为不可恢复，got %v", err)
	}
}

func TestHandleOutboundMessage_MissingGroupIDIsSkippable(t *testing.T) {
	b := newTestBridge()
	err := b.handleOutboundMessage(context.Background(), &types.MessageEvent{
		ChatID:   "qqgroup_1",
		ChatType: types.ChatTypeGroup,
		Content:  "hello",
		Metadata: map[string]string{types.MetaQQUserID: "10001", types.MetaQQSelfID: "20002"},
	})
	if !errors.Is(err, mq.ErrSkipMessage) {
		t.Fatalf("缺少群号应标记为不可恢复，got %v", err)
	}
}

func TestHandleOutboundMessage_EmptyMessageIsSkippable(t *testing.T) {
	b := newTestBridge()
	err := b.handleOutboundMessage(context.Background(), &types.MessageEvent{
		ChatID:   "chat-1",
		ChatType: types.ChatTypePrivate,
		Metadata: map[string]string{types.MetaQQUserID: "10001", types.MetaQQSelfID: "20002"},
	})
	if !errors.Is(err, mq.ErrSkipMessage) {
		t.Fatalf("空消息应标记为不可恢复，got %v", err)
	}
}

func TestHandleOutboundMessage_NoCallerIsRetryable(t *testing.T) {
	b := newTestBridge()
	// 没有建立 ZeroBot 连接时属于「QQ 暂时不可用」，必须可重试。
	err := b.handleOutboundMessage(context.Background(), &types.MessageEvent{
		ChatID:   "chat-1",
		ChatType: types.ChatTypePrivate,
		Content:  "hello",
		Metadata: map[string]string{types.MetaQQUserID: "10001", types.MetaQQSelfID: "20002"},
	})
	if err == nil {
		t.Fatal("QQ 未连接时应返回错误以触发重投")
	}
	if errors.Is(err, mq.ErrSkipMessage) {
		t.Fatal("QQ 未连接是可恢复错误，不能当成不可恢复跳过")
	}
}

// TestParseQQID 回归：旧写法 `groupID, _ := strconv.ParseInt(...)` 丢掉错误，
// 且只判 `== 0`，于是 "-123" 这种负数会一路下发到 ZeroBot 插件。
func TestParseQQID(t *testing.T) {
	cases := []struct {
		raw  string
		want int64
	}{
		{"10001", 10001},
		{" 10001 ", 10001},
		{"", 0},
		{"abc", 0},
		{"1.5", 0},
		{"0", 0},
		{"-123", 0},
		{"9223372036854775808", 0},
	}
	for _, c := range cases {
		t.Run(c.raw, func(t *testing.T) {
			if got := parseQQID(c.raw); got != c.want {
				t.Fatalf("parseQQID(%q) = %d，期望 %d", c.raw, got, c.want)
			}
		})
	}
}

func TestHandleOutboundMessage_NegativeIDsAreSkippable(t *testing.T) {
	b := newTestBridge()

	privateErr := b.handleOutboundMessage(context.Background(), &types.MessageEvent{
		ChatID:   "chat-1",
		ChatType: types.ChatTypePrivate,
		Content:  "hello",
		Metadata: map[string]string{
			types.MetaQQUserID: "-10001",
			types.MetaQQSelfID: "20002",
		},
	})
	if !errors.Is(privateErr, mq.ErrSkipMessage) {
		t.Fatalf("负数目标 QQ 号应标记为不可恢复，got %v", privateErr)
	}

	groupErr := b.handleOutboundMessage(context.Background(), &types.MessageEvent{
		ChatID:   "qqgroup_1",
		ChatType: types.ChatTypeGroup,
		Content:  "hello",
		Metadata: map[string]string{
			types.MetaQQUserID:  "10001",
			types.MetaQQSelfID:  "20002",
			types.MetaQQGroupID: "-30003",
		},
	})
	if !errors.Is(groupErr, mq.ErrSkipMessage) {
		t.Fatalf("负数群号应标记为不可恢复，got %v", groupErr)
	}
}
