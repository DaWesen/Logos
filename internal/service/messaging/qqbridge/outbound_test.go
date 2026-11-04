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
