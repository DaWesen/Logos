package service

import (
	"context"
	"testing"
	"time"

	"Logos/internal/service/messaging/types"
)

type panicBotClient struct {
	panicOn string
	called  chan<- string
}

func (c *panicBotClient) SendBotMessage(_ context.Context, botID, _, _, _ string, _ map[string]string) (string, error) {
	c.called <- botID
	if botID == c.panicOn {
		panic("bot client exploded")
	}
	return "ok", nil
}

func (c *panicBotClient) ResolveBotID(_ context.Context, botName string) (string, error) {
	return botName, nil
}

// TestInvokeBotForMention_PanicInOneBotDoesNotStopOthers 验证单个 Bot 调用 panic
// 不会拖垮进程，也不会阻断同一批次的其它 Bot。
// 修复前 panic 发生在裸 goroutine 里，未恢复会直接终止测试进程。
func TestInvokeBotForMention_PanicInOneBotDoesNotStopOthers(t *testing.T) {
	const (
		panicBotID = "11111111-1111-1111-1111-111111111111"
		okBotID    = "22222222-2222-2222-2222-222222222222"
	)

	called := make(chan string, 2)
	svc := &ChatServiceImpl{
		ctx:       context.Background(),
		botClient: &panicBotClient{panicOn: panicBotID, called: called},
		eventBus:  &types.EventBus{},
	}
	event := &types.MessageEvent{
		ID:       "async-1",
		ChatID:   "chat-1",
		ChatType: types.ChatTypePrivate,
		SenderID: "u1",
		Content:  "hello",
	}

	svc.invokeBotForMention(event, []string{panicBotID, okBotID})

	seen := map[string]bool{}
	for i := 0; i < 2; i++ {
		select {
		case botID := <-called:
			seen[botID] = true
		case <-time.After(5 * time.Second):
			t.Fatalf("等待 Bot 调用超时，已调用: %v", seen)
		}
	}
	if !seen[panicBotID] || !seen[okBotID] {
		t.Fatalf("两个 Bot 都应被调用，实际: %v", seen)
	}
}
