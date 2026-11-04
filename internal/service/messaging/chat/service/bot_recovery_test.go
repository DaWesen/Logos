package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"Logos/internal/service/messaging/chat/model"
	"Logos/internal/service/messaging/types"
	"Logos/pkg/mq"
)

const recoveryTestBotID = "11111111-1111-1111-1111-111111111111"

type stubBotClient struct {
	called chan string
	err    error
}

func (c *stubBotClient) SendBotMessage(_ context.Context, _, content, _, _ string, _ map[string]string) (string, error) {
	if c.called != nil {
		c.called <- content
	}
	return "ok", c.err
}

func (c *stubBotClient) ResolveBotID(_ context.Context, botName string) (string, error) {
	return botName, nil
}

func recoveryEvent() *types.MessageEvent {
	return &types.MessageEvent{
		ID:             "recovery-1",
		EventType:      types.EventTypeBotRecovery,
		ChatID:         "bot-" + recoveryTestBotID,
		ChatType:       types.ChatTypePrivate,
		SenderID:       "u1",
		Content:        "hello",
		MentionUserIDs: []string{"bot_" + recoveryTestBotID},
		Metadata: map[string]string{
			types.MetaQQFastRecovery: "true",
			types.MetaQQFastReason:   "bot_call_failed",
		},
	}
}

// TestHandleMessageEvent_BotRecoveryOnlyInvokesBot 验证补偿事件不会重新落库/推送。
// stubChatRepo 内嵌的是 nil 接口，任何 repo 调用都会 panic；
// 测试能跑通本身就说明补偿路径没有碰数据库。
func TestHandleMessageEvent_BotRecoveryOnlyInvokesBot(t *testing.T) {
	called := make(chan string, 1)
	svc := &ChatServiceImpl{
		repo:      &stubChatRepo{},
		eventBus:  &types.EventBus{},
		ctx:       context.Background(),
		botClient: &stubBotClient{called: called},
	}

	payload, err := json.Marshal(recoveryEvent())
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	if err := svc.HandleMessageEvent(&mq.Message{Topic: "chat.events", Value: payload}); err != nil {
		t.Fatalf("补偿事件不应返回错误: %v", err)
	}

	select {
	case content := <-called:
		if content != "hello" {
			t.Fatalf("应补跑原始查询，实际 %q", content)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("补偿事件没有触发 Bot 调用")
	}
}

func TestHandleBotRecoveryEvent_RequiresMarker(t *testing.T) {
	called := make(chan string, 1)
	svc := &ChatServiceImpl{
		repo:      &stubChatRepo{},
		eventBus:  &types.EventBus{},
		ctx:       context.Background(),
		botClient: &stubBotClient{called: called},
	}

	event := recoveryEvent()
	delete(event.Metadata, types.MetaQQFastRecovery)
	if err := svc.handleBotRecoveryEvent(event); err != nil {
		t.Fatalf("缺少标记应安静忽略，实际 %v", err)
	}
	select {
	case <-called:
		t.Fatal("没有补偿标记的事件不应触发 Bot 调用")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestHandleBotRecoveryEvent_NoBotClientIsNoop(t *testing.T) {
	svc := &ChatServiceImpl{repo: &stubChatRepo{}, ctx: context.Background()}
	if err := svc.handleBotRecoveryEvent(recoveryEvent()); err != nil {
		t.Fatalf("Bot 客户端缺失时应跳过而不是报错: %v", err)
	}
}

func TestHandleBotRecoveryEvent_NilEventIsNoop(t *testing.T) {
	svc := &ChatServiceImpl{repo: &stubChatRepo{}, ctx: context.Background()}
	if err := svc.handleBotRecoveryEvent(nil); err != nil {
		t.Fatalf("nil 事件应安全忽略: %v", err)
	}
}

// TestHandleBotRecoveryEvent_SkipsWhenReplyAlreadyPersisted
// 补偿事件被重投时，如果这条回复已经落库，就不该再调一次 Bot（有成本）。
func TestHandleBotRecoveryEvent_SkipsWhenReplyAlreadyPersisted(t *testing.T) {
	called := make(chan string, 1)
	event := recoveryEvent()
	replyID := types.BotReplyMessageID(recoveryTestBotID, event.ID)
	if replyID == "" {
		t.Fatal("测试前置：应能派生回复 ID")
	}

	repo := &stubChatRepo{messages: map[string]*model.Message{replyID: {ID: replyID}}}
	svc := &ChatServiceImpl{
		repo:      repo,
		ctx:       context.Background(),
		botClient: &stubBotClient{called: called},
	}

	if err := svc.handleBotRecoveryEvent(event); err != nil {
		t.Fatalf("补偿应安静跳过，实际 %v", err)
	}
	select {
	case <-called:
		t.Fatal("回复已落库时不应重复调用 Bot")
	case <-time.After(200 * time.Millisecond):
	}
}

// TestBotReplyAlreadyProcessed_OnlyGuardsBotReplies
// 守卫只应作用于 Bot 回复；普通用户消息不能被它拦住，否则用户发第二条
// 相同内容的消息会被误判成重复。
func TestBotReplyAlreadyProcessed_OnlyGuardsBotReplies(t *testing.T) {
	repo := &stubChatRepo{messages: map[string]*model.Message{"m1": {ID: "m1"}}}
	svc := &ChatServiceImpl{repo: repo, ctx: context.Background()}

	if svc.botReplyAlreadyProcessed(&types.MessageEvent{ID: "m1", SenderID: "u1"}) {
		t.Fatal("普通用户消息不应被 Bot 回复守卫拦截")
	}
	if svc.botReplyAlreadyProcessed(nil) {
		t.Fatal("nil 事件应返回 false")
	}
	if !svc.botReplyAlreadyProcessed(&types.MessageEvent{ID: "m1", SenderID: "bot_b1"}) {
		t.Fatal("已落库的 Bot 回复应判定为已处理")
	}
	if svc.botReplyAlreadyProcessed(&types.MessageEvent{ID: "m2", SenderID: "bot_b1"}) {
		t.Fatal("未落库的 Bot 回复应判定为未处理")
	}
}

func TestBotReplyAlreadyProcessed_ReadFailureFailsOpen(t *testing.T) {
	// 读失败必须按「未处理」继续：宁可多一次主键写冲突，
	// 也不能因为一次查询失败把正常回复吞掉。
	svc := &ChatServiceImpl{repo: &erroringMessageRepo{}, ctx: context.Background()}
	if svc.botReplyAlreadyProcessed(&types.MessageEvent{ID: "m1", SenderID: "bot_b1"}) {
		t.Fatal("查询失败时应失败开放，而不是判定为已处理")
	}
}

type erroringMessageRepo struct {
	*stubChatRepo
}

var errLookupFailed = errors.New("lookup failed")

func (r *erroringMessageRepo) GetMessageByID(context.Context, string) (*model.Message, error) {
	return nil, errLookupFailed
}
