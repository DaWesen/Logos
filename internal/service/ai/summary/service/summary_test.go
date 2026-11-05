package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"Logos/internal/service/ai/summary/dao"
	summarymodel "Logos/internal/service/ai/summary/model"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type fakeChatService struct {
	msgs  []*ChatMessage
	err   error
	calls int
}

func (f *fakeChatService) GetMessageHistory(ctx context.Context, chatID string, limit int, beforeTime *time.Time) ([]*ChatMessage, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.msgs, nil
}

type fakeSummaryRepo struct {
	created   []*summarymodel.SummaryRecord
	createErr error
}

var _ dao.SummaryRepository = (*fakeSummaryRepo)(nil)

func (f *fakeSummaryRepo) CreateSummary(ctx context.Context, record *summarymodel.SummaryRecord) error {
	if f.createErr != nil {
		return f.createErr
	}
	f.created = append(f.created, record)
	return nil
}

func (f *fakeSummaryRepo) GetSummary(ctx context.Context, id string) (*summarymodel.SummaryRecord, error) {
	return nil, nil
}

func (f *fakeSummaryRepo) ListSummaries(ctx context.Context, chatID string, page, pageSize int) ([]*summarymodel.SummaryRecord, int64, error) {
	return nil, 0, nil
}

// scriptedChatModel 按提示词关键字返回脚本化结果，避免单测依赖真实模型。
type scriptedChatModel struct {
	calls   int
	respond func(prompt string) (string, error)
}

func (m *scriptedChatModel) Generate(ctx context.Context, input []*schema.Message, opts ...einomodel.Option) (*schema.Message, error) {
	m.calls++
	prompt := ""
	for i := len(input) - 1; i >= 0; i-- {
		if input[i].Role == schema.User {
			prompt = input[i].Content
			break
		}
	}
	if m.respond != nil {
		content, err := m.respond(prompt)
		if err != nil {
			return nil, err
		}
		return schema.AssistantMessage(content, nil), nil
	}
	content, err := defaultScriptedResponse(prompt)
	if err != nil {
		return nil, err
	}
	return schema.AssistantMessage(content, nil), nil
}

func (m *scriptedChatModel) Stream(ctx context.Context, input []*schema.Message, opts ...einomodel.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}

func defaultScriptedResponse(prompt string) (string, error) {
	switch {
	case strings.Contains(prompt, "提取关键要点"):
		return `["要点一","要点二"]`, nil
	case strings.Contains(prompt, "提取所有参与者"):
		return `["alice","bob"]`, nil
	case strings.Contains(prompt, "提取所有待办事项"):
		return `[{"id":"t1","content":"准备发版","assignee":"alice","deadline":"","status":"pending"}]`, nil
	case strings.Contains(prompt, "回复候选"):
		return `[{"id":"c1","content":"好的，我稍后同步","confidence":0.9,"type":"agreement"}]`, nil
	default:
		return "本次讨论确认了发版时间与负责人。", nil
	}
}

func newTestService(chat ChatService, repo dao.SummaryRepository, model *scriptedChatModel) *summaryServiceImpl {
	return &summaryServiceImpl{
		repo:        repo,
		chatService: chat,
		newChatModel: func(*ModelConfig) (einomodel.BaseChatModel, error) {
			return model, nil
		},
	}
}

func sampleMessages() []*ChatMessage {
	return []*ChatMessage{
		{ID: "m1", ChatID: "chat-1", SenderID: "alice", Content: "周五发版可以吗"},
		{ID: "m2", ChatID: "chat-1", SenderID: "bob", Content: "可以，我来准备发布说明"},
	}
}

func TestSummarizeMessages_HistoryFailureReturnsError(t *testing.T) {
	chat := &fakeChatService{err: errors.New("chat 服务不可用")}
	repo := &fakeSummaryRepo{}
	model := &scriptedChatModel{}
	svc := newTestService(chat, repo, model)

	record, _, _, _, _, err := svc.SummarizeMessages(context.Background(), "chat-1", "private", nil, false, false, 50, nil)
	if err == nil {
		t.Fatal("历史拉取失败时应返回错误，而不是拿占位上下文生成摘要")
	}
	if !strings.Contains(err.Error(), "拉取消息历史失败") {
		t.Fatalf("错误信息应包含上下文，got %v", err)
	}
	if record != nil {
		t.Fatalf("失败时不应返回记录，got %+v", record)
	}
	if model.calls != 0 {
		t.Fatalf("历史拉取失败时不应调用模型，calls = %d", model.calls)
	}
	if len(repo.created) != 0 {
		t.Fatalf("失败时不应落库，created = %d", len(repo.created))
	}
}

func TestSummarizeMessages_EmptyHistoryReturnsError(t *testing.T) {
	chat := &fakeChatService{msgs: []*ChatMessage{{ID: "m1", SenderID: "alice", Content: "   "}}}
	model := &scriptedChatModel{}
	svc := newTestService(chat, &fakeSummaryRepo{}, model)

	_, _, _, _, _, err := svc.SummarizeMessages(context.Background(), "chat-1", "private", nil, false, false, 50, nil)
	if err == nil {
		t.Fatal("没有可用消息时应返回错误")
	}
	if !strings.Contains(err.Error(), "没有可用的消息内容") {
		t.Fatalf("错误信息应说明原因，got %v", err)
	}
	if model.calls != 0 {
		t.Fatalf("空历史时不应调用模型，calls = %d", model.calls)
	}
}

func TestSummarizeMessages_TodoExtractionFailureReturnsError(t *testing.T) {
	chat := &fakeChatService{msgs: sampleMessages()}
	model := &scriptedChatModel{respond: func(prompt string) (string, error) {
		if strings.Contains(prompt, "提取所有待办事项") {
			return "", errors.New("模型超时")
		}
		return defaultScriptedResponse(prompt)
	}}
	repo := &fakeSummaryRepo{}
	svc := newTestService(chat, repo, model)

	_, _, _, _, _, err := svc.SummarizeMessages(context.Background(), "chat-1", "private", nil, true, false, 50, nil)
	if err == nil {
		t.Fatal("显式要求提取待办时，提取失败不能返回成功 + 空列表")
	}
	if !strings.Contains(err.Error(), "提取待办事项失败") {
		t.Fatalf("错误信息应包含阶段，got %v", err)
	}
	if len(repo.created) != 0 {
		t.Fatalf("半成品摘要不应落库，created = %d", len(repo.created))
	}
}

func TestSummarizeMessages_KeyPointsFailureDegradesButSucceeds(t *testing.T) {
	chat := &fakeChatService{msgs: sampleMessages()}
	model := &scriptedChatModel{respond: func(prompt string) (string, error) {
		if strings.Contains(prompt, "提取关键要点") {
			return "", errors.New("要点提取模型故障")
		}
		return defaultScriptedResponse(prompt)
	}}
	repo := &fakeSummaryRepo{}
	svc := newTestService(chat, repo, model)

	record, _, _, keyPoints, participants, err := svc.SummarizeMessages(context.Background(), "chat-1", "private", nil, false, false, 50, nil)
	if err != nil {
		t.Fatalf("要点属于增强项，失败不应打断摘要: %v", err)
	}
	if len(keyPoints) != 0 {
		t.Fatalf("要点提取失败时应为空，got %v", keyPoints)
	}
	if len(participants) != 2 {
		t.Fatalf("参与者不应受要点失败影响，got %v", participants)
	}
	if record == nil || record.KeyPoints != "null" {
		t.Fatalf("记录应如实写入空要点，got %+v", record)
	}
	if len(repo.created) != 1 {
		t.Fatalf("成功路径应落库一次，created = %d", len(repo.created))
	}
}

func TestSummarizeMessages_PersistFailureReturnsError(t *testing.T) {
	chat := &fakeChatService{msgs: sampleMessages()}
	repo := &fakeSummaryRepo{createErr: errors.New("db 写入超时")}
	svc := newTestService(chat, repo, &scriptedChatModel{})

	record, _, _, _, _, err := svc.SummarizeMessages(context.Background(), "chat-1", "private", nil, false, false, 50, nil)
	if err == nil {
		t.Fatal("记录未落库时不能返回成功")
	}
	if !strings.Contains(err.Error(), "保存摘要记录失败") {
		t.Fatalf("错误信息应包含阶段，got %v", err)
	}
	if record != nil {
		t.Fatalf("落库失败时不应返回记录，got %+v", record)
	}
}

func TestGenerateReplyCandidates_HistoryFailureReturnsError(t *testing.T) {
	chat := &fakeChatService{err: errors.New("chat 服务不可用")}
	model := &scriptedChatModel{}
	svc := newTestService(chat, &fakeSummaryRepo{}, model)

	candidates, err := svc.GenerateReplyCandidates(context.Background(), "chat-1", "private", nil, 3, "friendly", nil)
	if err == nil {
		t.Fatal("历史拉取失败时应返回错误")
	}
	if candidates != nil {
		t.Fatalf("失败时不应返回候选，got %v", candidates)
	}
	if model.calls != 0 {
		t.Fatalf("历史拉取失败时不应调用模型，calls = %d", model.calls)
	}
}

func TestGenerateReplyCandidates_ParseFailureReturnsError(t *testing.T) {
	chat := &fakeChatService{msgs: sampleMessages()}
	model := &scriptedChatModel{respond: func(string) (string, error) {
		return "这不是 JSON", nil
	}}
	svc := newTestService(chat, &fakeSummaryRepo{}, model)

	candidates, err := svc.GenerateReplyCandidates(context.Background(), "chat-1", "private", nil, 3, "friendly", nil)
	if err == nil {
		t.Fatal("候选解析失败时应返回错误，而不是空列表 + 成功")
	}
	if candidates != nil {
		t.Fatalf("失败时不应返回候选，got %v", candidates)
	}
}

func TestExtractTodos_HistoryFailureReturnsError(t *testing.T) {
	chat := &fakeChatService{err: errors.New("chat 服务不可用")}
	model := &scriptedChatModel{}
	svc := newTestService(chat, &fakeSummaryRepo{}, model)

	todos, err := svc.ExtractTodos(context.Background(), "chat-1", "private", nil, 50, nil)
	if err == nil {
		t.Fatal("历史拉取失败时应返回错误")
	}
	if todos != nil {
		t.Fatalf("失败时不应返回待办，got %v", todos)
	}
	if model.calls != 0 {
		t.Fatalf("历史拉取失败时不应调用模型，calls = %d", model.calls)
	}
}

func TestExtractTodos_ParseFailureReturnsError(t *testing.T) {
	chat := &fakeChatService{msgs: sampleMessages()}
	model := &scriptedChatModel{respond: func(string) (string, error) {
		return "没有待办", nil
	}}
	svc := newTestService(chat, &fakeSummaryRepo{}, model)

	todos, err := svc.ExtractTodos(context.Background(), "chat-1", "private", nil, 50, nil)
	if err == nil {
		t.Fatal("待办解析失败时应返回错误")
	}
	if todos != nil {
		t.Fatalf("失败时不应返回待办，got %v", todos)
	}
}

func TestSummarizeMessages_MessageIDsFallBackToHistory(t *testing.T) {
	chat := &fakeChatService{msgs: sampleMessages()}
	repo := &fakeSummaryRepo{}
	svc := newTestService(chat, repo, &scriptedChatModel{})

	record, _, _, _, _, err := svc.SummarizeMessages(context.Background(), "chat-1", "private", []string{"m1", "m2"}, false, false, 50, nil)
	if err != nil {
		t.Fatalf("按 ID 取内容未实现时应回退历史而不是失败: %v", err)
	}
	if chat.calls != 1 {
		t.Fatalf("应回退拉取一次历史，calls = %d", chat.calls)
	}
	if record == nil || record.MessageIDs != `["m1","m2"]` {
		t.Fatalf("请求的消息 ID 应原样留档，got %+v", record)
	}
}

func TestMarshalField_ReturnsError(t *testing.T) {
	if _, err := marshalField("broken", make(chan int)); err == nil {
		t.Fatal("不可序列化的值应返回错误，而不是写入空字段")
	}
}
