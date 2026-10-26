package service

import (
	"context"
	"testing"
	"time"

	"Logos/internal/service/ai/bot/dao"
	botmodel "Logos/internal/service/ai/bot/model"
	"Logos/pkg/auth"
	"Logos/pkg/cache"

	"google.golang.org/grpc/metadata"
)

func TestGetHistory_Ownership(t *testing.T) {
	svc, repo, _ := newOwnershipFixture()

	// 他人会话：直接拒绝，且不得触达消息表
	if _, _, err := svc.GetHistory(userCtx("alice"), "bot-owner", "conv-bob", 20, nil); err != ErrConversationNotFound {
		t.Fatalf("越权读取历史应返回 ErrConversationNotFound，实际 %v", err)
	}
	if len(repo.messagesQueried) != 0 {
		t.Fatalf("越权请求不应查询消息，实际查询 %v", repo.messagesQueried)
	}

	// 未认证
	if _, _, err := svc.GetHistory(context.Background(), "bot-owner", "conv-history-alice", 20, nil); err != ErrConversationNotFound {
		t.Fatalf("未认证读取历史应被拒绝，实际 %v", err)
	}

	// Bot 与会话不匹配：防止用别人的 BotID 套自己的会话
	if _, _, err := svc.GetHistory(userCtx("alice"), "bot-other", "conv-history-alice", 20, nil); err != ErrConversationNotFound {
		t.Fatalf("BotID 不匹配应被拒绝，实际 %v", err)
	}

	// 会话不存在
	if _, _, err := svc.GetHistory(userCtx("alice"), "bot-owner", "not-exist", 20, nil); err != ErrConversationNotFound {
		t.Fatalf("不存在的会话应返回 ErrConversationNotFound，实际 %v", err)
	}

	// 所有者 + Bot 匹配：放行
	msgs, hasMore, err := svc.GetHistory(userCtx("alice"), "bot-owner", "conv-history-alice", 20, nil)
	if err != nil {
		t.Fatalf("所有者读取历史应成功，实际 %v", err)
	}
	if len(msgs) != 1 || hasMore {
		t.Fatalf("返回结果不符：msgs=%d hasMore=%v", len(msgs), hasMore)
	}
	if len(repo.messagesQueried) != 1 {
		t.Fatalf("应只查询一次消息，实际 %v", repo.messagesQueried)
	}
}

// stubRepo 只实现归属校验用到的方法；其余方法由嵌入的 nil 接口兜底，
// 一旦被测代码误调用未实现的方法会立刻 panic，避免测试静默通过。
type stubRepo struct {
	dao.BotRepository

	bots          map[string]*botmodel.Bot
	conversations map[string]*botmodel.Conversation
	prompts       map[string]*botmodel.Prompt

	deletedBots    []string
	updatedBots    []*botmodel.Bot
	deletedConvs   []string
	updatedConvs   []*botmodel.Conversation
	deletedPrompts []string
	updatedPrompts []*botmodel.Prompt

	messages        []*botmodel.Message
	messagesQueried []string
}

func (r *stubRepo) GetMessages(_ context.Context, conversationID string, _ int, _ *time.Time) ([]*botmodel.Message, error) {
	r.messagesQueried = append(r.messagesQueried, conversationID)
	return r.messages, nil
}

func (r *stubRepo) GetBot(_ context.Context, id string) (*botmodel.Bot, error) {
	return r.bots[id], nil
}

func (r *stubRepo) UpdateBot(_ context.Context, bot *botmodel.Bot) error {
	r.updatedBots = append(r.updatedBots, bot)
	return nil
}

func (r *stubRepo) DeleteBot(_ context.Context, id string) error {
	r.deletedBots = append(r.deletedBots, id)
	return nil
}

func (r *stubRepo) GetConversation(_ context.Context, id string) (*botmodel.Conversation, error) {
	return r.conversations[id], nil
}

func (r *stubRepo) UpdateConversation(_ context.Context, c *botmodel.Conversation) error {
	r.updatedConvs = append(r.updatedConvs, c)
	return nil
}

func (r *stubRepo) DeleteConversation(_ context.Context, id string) error {
	r.deletedConvs = append(r.deletedConvs, id)
	return nil
}

func (r *stubRepo) GetPrompt(_ context.Context, id string) (*botmodel.Prompt, error) {
	return r.prompts[id], nil
}

func (r *stubRepo) UpdatePrompt(_ context.Context, p *botmodel.Prompt) error {
	r.updatedPrompts = append(r.updatedPrompts, p)
	return nil
}

func (r *stubRepo) DeletePrompt(_ context.Context, id string) error {
	r.deletedPrompts = append(r.deletedPrompts, id)
	return nil
}

// stubCache 只实现测试用到的方法；其余方法由嵌入的 nil 接口兜底。
type stubCache struct {
	cache.Cache
	deleted []string
}

func (c *stubCache) Delete(_ context.Context, key string) error {
	c.deleted = append(c.deleted, key)
	return nil
}

// userCtx 构造带 user_id 的入站 gRPC context：
// 归属校验读取的是 incoming metadata（网关以 outgoing metadata 发出，抵达服务端后即为 incoming）。
func userCtx(uid string) context.Context {
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs(auth.UserIDKey, uid))
}

func newOwnershipFixture() (*botServiceImpl, *stubRepo, *stubCache) {
	c := &stubCache{}
	repo := &stubRepo{
		bots: map[string]*botmodel.Bot{
			"bot-owner":  {ID: "bot-owner", UserID: "alice", APIKey: "sk-alice", BaseURL: "https://alice.example"},
			"bot-other":  {ID: "bot-other", UserID: "bob", APIKey: "sk-bob"},
			"bot-noqq":   {ID: "bot-noqq", UserID: "alice"},
			"bot-withqq": {ID: "bot-withqq", UserID: "alice", QQNumber: "10001"},
		},
		conversations: map[string]*botmodel.Conversation{
			"conv-alice":         {ID: "conv-alice", UserID: "alice", Title: "old"},
			"conv-bob":           {ID: "conv-bob", UserID: "bob", Title: "bob's"},
			"conv-history-alice": {ID: "conv-history-alice", UserID: "alice", BotID: "bot-owner"},
		},
		prompts: map[string]*botmodel.Prompt{
			"prompt-alice":  {ID: "prompt-alice", UserID: "alice", Name: "private"},
			"prompt-bob":    {ID: "prompt-bob", UserID: "bob", Name: "bob private"},
			"prompt-public": {ID: "prompt-public", UserID: "bob", Name: "public", IsPublic: true},
			"prompt-preset": {ID: "prompt-preset", UserID: "system", Name: "preset", IsPreset: true},
		},
	}
	// 被测方法只依赖 repo 与 QQ 路由缓存（其余字段为 nil 时各分支均已做 nil 判断）
	repo.messages = []*botmodel.Message{{ID: "m1", ConversationID: "conv-history-alice"}}
	return &botServiceImpl{repo: repo, qqBindCache: c}, repo, c
}

func TestGetBot_NonOwnerGetsNotFound(t *testing.T) {
	svc, _, _ := newOwnershipFixture()

	got, err := svc.GetBot(userCtx("alice"), "bot-other")
	if err != nil {
		t.Fatalf("GetBot returned error: %v", err)
	}
	if got != nil {
		t.Fatalf("越权读取应返回 nil，实际返回 %+v（含 APIKey 泄露风险）", got)
	}
}

func TestGetBot_OwnerReadsOwnBot(t *testing.T) {
	svc, _, _ := newOwnershipFixture()

	got, err := svc.GetBot(userCtx("alice"), "bot-owner")
	if err != nil {
		t.Fatalf("GetBot returned error: %v", err)
	}
	if got == nil || got.ID != "bot-owner" {
		t.Fatalf("所有者应能读到自己的 Bot，实际 got=%+v", got)
	}
}

func TestGetBot_UnauthenticatedDenied(t *testing.T) {
	svc, _, _ := newOwnershipFixture()

	got, err := svc.GetBot(context.Background(), "bot-owner")
	if err != nil {
		t.Fatalf("GetBot returned error: %v", err)
	}
	if got != nil {
		t.Fatalf("缺少 user_id 时应按未认证拒绝，实际返回 %+v", got)
	}
}

func TestUpdateBot_NonOwnerRejected(t *testing.T) {
	svc, repo, _ := newOwnershipFixture()

	_, err := svc.UpdateBot(userCtx("alice"), "alice", "bot-other", "hijack", "", "", "", "", "", "", "", nil, "")
	if err != ErrBotNotFound {
		t.Fatalf("越权更新应返回 ErrBotNotFound，实际 %v", err)
	}
	if len(repo.updatedBots) != 0 {
		t.Fatalf("越权更新不应写入数据库，实际写入 %d 条", len(repo.updatedBots))
	}
}

func TestUpdateBot_OwnerSucceeds(t *testing.T) {
	svc, repo, _ := newOwnershipFixture()

	got, err := svc.UpdateBot(userCtx("alice"), "alice", "bot-noqq", "newname", "", "", "", "", "", "", "", nil, "")
	if err != nil {
		t.Fatalf("所有者更新应成功，实际错误 %v", err)
	}
	if got == nil || got.Name != "newname" {
		t.Fatalf("更新后名称应为 newname，实际 %+v", got)
	}
	if len(repo.updatedBots) != 1 {
		t.Fatalf("应写入 1 次，实际 %d 次", len(repo.updatedBots))
	}
}

func TestDeleteBot_NonOwnerRejected(t *testing.T) {
	svc, repo, _ := newOwnershipFixture()

	if err := svc.DeleteBot(userCtx("alice"), "bot-other"); err != ErrBotNotFound {
		t.Fatalf("越权删除应返回 ErrBotNotFound，实际 %v", err)
	}
	if len(repo.deletedBots) != 0 {
		t.Fatalf("越权删除不应落库，实际删除 %v", repo.deletedBots)
	}
}

func TestDeleteBot_OwnerDeletesAndUnbindsQQ(t *testing.T) {
	svc, repo, c := newOwnershipFixture()

	if err := svc.DeleteBot(userCtx("alice"), "bot-withqq"); err != nil {
		t.Fatalf("所有者删除应成功，实际错误 %v", err)
	}
	if len(repo.updatedBots) != 1 || repo.updatedBots[0].QQNumber != "" {
		t.Fatalf("删除前应清空 QQ 绑定（避免唯一索引冲突），实际 %+v", repo.updatedBots)
	}
	if len(repo.deletedBots) != 1 || repo.deletedBots[0] != "bot-withqq" {
		t.Fatalf("应删除目标 Bot，实际 %v", repo.deletedBots)
	}
	// 路由缓存必须同步清理，否则 QQ 消息还会被路由到已删除的 Bot
	if len(c.deleted) != 1 || c.deleted[0] != "qq:bot_bind:10001" {
		t.Fatalf("应删除 qq:bot_bind:10001 缓存，实际 %v", c.deleted)
	}
}

func TestDeleteBot_MissingReturnsNil(t *testing.T) {
	svc, repo, _ := newOwnershipFixture()

	if err := svc.DeleteBot(userCtx("alice"), "not-exist"); err != nil {
		t.Fatalf("删除不存在的 Bot 应幂等返回 nil，实际 %v", err)
	}
	if len(repo.deletedBots) != 0 {
		t.Fatalf("不应触发删除，实际 %v", repo.deletedBots)
	}
}

func TestConversation_Ownership(t *testing.T) {
	svc, repo, _ := newOwnershipFixture()

	// 非所有者读 → nil
	if got, err := svc.GetConversation(userCtx("alice"), "conv-bob"); err != nil || got != nil {
		t.Fatalf("越权读取对话应返回 nil,nil，实际 %+v,%v", got, err)
	}
	// 非所有者改 → ErrConversationNotFound
	if _, err := svc.UpdateConversation(userCtx("alice"), "conv-bob", "hijack"); err != ErrConversationNotFound {
		t.Fatalf("越权更新对话应返回 ErrConversationNotFound，实际 %v", err)
	}
	// 非所有者删 → ErrConversationNotFound
	if err := svc.DeleteConversation(userCtx("alice"), "conv-bob"); err != ErrConversationNotFound {
		t.Fatalf("越权删除对话应返回 ErrConversationNotFound，实际 %v", err)
	}
	if len(repo.updatedConvs) != 0 || len(repo.deletedConvs) != 0 {
		t.Fatalf("越权操作不应落库：updated=%d deleted=%d", len(repo.updatedConvs), len(repo.deletedConvs))
	}

	// 所有者可改
	got, err := svc.UpdateConversation(userCtx("alice"), "conv-alice", "renamed")
	if err != nil || got == nil || got.Title != "renamed" {
		t.Fatalf("所有者更新对话失败：got=%+v err=%v", got, err)
	}
}

func TestPrompt_VisibilityRules(t *testing.T) {
	svc, repo, _ := newOwnershipFixture()

	// 私有提示词：仅创建者可读
	if got, _ := svc.GetPrompt(userCtx("alice"), "prompt-bob"); got != nil {
		t.Fatalf("他人私有提示词应不可读，实际返回 %+v", got)
	}
	if got, _ := svc.GetPrompt(userCtx("alice"), "prompt-alice"); got == nil {
		t.Fatalf("创建者应可读自己的私有提示词")
	}
	// 公开/预设提示词：所有登录用户可读
	if got, _ := svc.GetPrompt(userCtx("alice"), "prompt-public"); got == nil {
		t.Fatalf("公开提示词应可读")
	}
	if got, _ := svc.GetPrompt(userCtx("alice"), "prompt-preset"); got == nil {
		t.Fatalf("预设提示词应可读")
	}
	// 未认证不得读取私有提示词
	if got, _ := svc.GetPrompt(context.Background(), "prompt-alice"); got != nil {
		t.Fatalf("未认证不应读到私有提示词")
	}

	// 公开提示词也只有创建者能改/删
	if _, err := svc.UpdatePrompt(userCtx("alice"), "prompt-public", "hijack", "", "", nil, nil); err != ErrPromptNotFound {
		t.Fatalf("非创建者更新公开提示词应返回 ErrPromptNotFound，实际 %v", err)
	}
	if err := svc.DeletePrompt(userCtx("alice"), "prompt-public"); err != ErrPromptNotFound {
		t.Fatalf("非创建者删除公开提示词应返回 ErrPromptNotFound，实际 %v", err)
	}
	if len(repo.updatedPrompts) != 0 || len(repo.deletedPrompts) != 0 {
		t.Fatalf("越权提示词操作不应落库：updated=%d deleted=%d", len(repo.updatedPrompts), len(repo.deletedPrompts))
	}
}
