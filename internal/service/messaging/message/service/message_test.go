package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"Logos/internal/service/messaging/message/dao"
	"Logos/pkg/mq"
)

type fakeAnswer struct{}

func (fakeAnswer) GetID() string { return "answer-1" }

type fakeQuestionService struct {
	calls  int
	err    error
	gotCtx context.Context
	ctxErr error
	block  bool
}

func (f *fakeQuestionService) AskQuestion(ctx context.Context, _ string, _ *string) (interface{ GetID() string }, error) {
	f.calls++
	f.gotCtx = ctx
	f.ctxErr = ctx.Err()
	if f.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return fakeAnswer{}, f.err
}

type stubMessageRepo struct {
	dao.MessageRepository
	batchCalls int
	lastIDs    []string
	lastStatus string
	batchErr   error
	updateErr  error
}

func (s *stubMessageRepo) UpdateMessagesStatus(_ context.Context, ids []string, status string) error {
	s.batchCalls++
	s.lastIDs = append([]string(nil), ids...)
	s.lastStatus = status
	return s.batchErr
}

func (s *stubMessageRepo) UpdateMessageStatus(_ context.Context, _ string, _ string) error {
	return s.updateErr
}

// 内部 topic 名与 Kafka topic 必须一一对应。历史上 outbox 直接把
// "QA_REQUEST" 当 Kafka topic 发，而消费者订阅的是 "qa_request"，
// 消息显示投递成功却永远不会被处理。
func TestKafkaTopicFor_MapsInternalNames(t *testing.T) {
	cases := map[string]string{
		"DATA_COLLECTION":      mq.TopicDataCollection,
		"KNOWLEDGE_EXTRACTION": mq.TopicKnowledgeExtraction,
		"VECTOR_PROCESSING":    mq.TopicVectorProcessing,
		"QA_REQUEST":           mq.TopicQARequest,
		"RECOMMENDATION":       mq.TopicRecommendation,
		"USER_ACTIVITY":        mq.TopicUserActivity,
		"SYSTEM_EVENT":         mq.TopicSystemEvent,
		"":                     mq.TopicSystemEvent,
		"unknown":              mq.TopicSystemEvent,
	}
	for internal, want := range cases {
		if got := kafkaTopicFor(internal); got != want {
			t.Fatalf("kafkaTopicFor(%q) = %q, want %q", internal, got, want)
		}
	}
}

func TestHandleGenericMessage_QAErrorIsReturnedForRetry(t *testing.T) {
	question := &fakeQuestionService{err: errors.New("llm unavailable")}
	svc := &messageServiceImpl{questionService: question}

	msg := &mq.Message{
		Topic:   mq.TopicQARequest,
		Value:   []byte(`{"content":"hello"}`),
		Context: context.Background(),
	}
	if err := svc.handleGenericMessage(msg); err == nil {
		t.Fatal("问答失败必须返回错误，否则 offset 会被提交、消息永久丢失")
	}
	if question.calls != 1 {
		t.Fatalf("AskQuestion 调用次数 = %d, want 1", question.calls)
	}
}

func TestHandleGenericMessage_QAUsesBoundedChildContext(t *testing.T) {
	question := &fakeQuestionService{}
	svc := &messageServiceImpl{questionService: question}

	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	msg := &mq.Message{
		Topic:   mq.TopicQARequest,
		Value:   []byte(`{"content":"hello"}`),
		Context: parent,
	}
	if err := svc.handleGenericMessage(msg); err != nil {
		t.Fatalf("正常问答不应报错: %v", err)
	}
	if question.gotCtx == nil {
		t.Fatal("AskQuestion 没有收到 ctx")
	}
	if _, ok := question.gotCtx.Deadline(); !ok {
		t.Fatal("问答应带独立超时，避免一条消息永久占住分区")
	}
	if question.ctxErr != nil {
		t.Fatalf("父 ctx 正常时调用瞬间的子 ctx 不应已取消: %v", question.ctxErr)
	}
}

func TestHandleGenericMessage_QAStopsOnLifecycleCancellation(t *testing.T) {
	question := &fakeQuestionService{block: true}
	svc := &messageServiceImpl{questionService: question}

	parent, cancel := context.WithCancel(context.Background())
	cancel()
	msg := &mq.Message{
		Topic:   mq.TopicQARequest,
		Value:   []byte(`{"content":"hello"}`),
		Context: parent,
	}

	done := make(chan error, 1)
	go func() { done <- svc.handleGenericMessage(msg) }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("生命周期 ctx 已取消时应返回错误")
		}
	case <-time.After(time.Second):
		t.Fatal("生命周期 ctx 取消后 handler 没有退出")
	}
}

func TestHandleGenericMessage_NonQADoesNotCallQuestionService(t *testing.T) {
	question := &fakeQuestionService{}
	svc := &messageServiceImpl{questionService: question}

	msg := &mq.Message{
		Topic:   mq.TopicRecommendation,
		Value:   []byte(`{"content":"hello"}`),
		Context: context.Background(),
	}
	if err := svc.handleGenericMessage(msg); err != nil {
		t.Fatalf("非问答消息不应报错: %v", err)
	}
	if question.calls != 0 {
		t.Fatalf("非问答消息不应触发 AskQuestion, calls = %d", question.calls)
	}
}

// 批量确认必须一条 SQL 完成，并过滤空/重复 id；失败要上抛而不是当成成功。
func TestBatchAcknowledgeMessages_SingleBatchAndErrorPropagation(t *testing.T) {
	repo := &stubMessageRepo{}
	svc := &messageServiceImpl{repo: repo}

	if err := svc.BatchAcknowledgeMessages(context.Background(), "g", []string{"a", "", "b", "a"}, 4); err != nil {
		t.Fatalf("正常批量确认不应报错: %v", err)
	}
	if repo.batchCalls != 1 {
		t.Fatalf("应只发一条批量 UPDATE, calls = %d", repo.batchCalls)
	}
	if repo.lastStatus != "ACKED" {
		t.Fatalf("status = %q, want ACKED", repo.lastStatus)
	}
	if len(repo.lastIDs) != 2 || repo.lastIDs[0] != "a" || repo.lastIDs[1] != "b" {
		t.Fatalf("id 去重/过滤结果不符: %v", repo.lastIDs)
	}

	repo.batchErr = errors.New("db down")
	if err := svc.BatchAcknowledgeMessages(context.Background(), "g", []string{"a"}, 4); err == nil {
		t.Fatal("批量更新失败必须返回错误，不能被 continue 吞掉")
	}
}

func TestBatchAcknowledgeMessages_EmptyIDsSkipsDatabase(t *testing.T) {
	repo := &stubMessageRepo{}
	svc := &messageServiceImpl{repo: repo}

	if err := svc.BatchAcknowledgeMessages(context.Background(), "g", []string{"", ""}, 4); err != nil {
		t.Fatalf("空批次不应报错: %v", err)
	}
	if repo.batchCalls != 0 {
		t.Fatalf("没有有效 id 时不应访问数据库, calls = %d", repo.batchCalls)
	}
}

// 处理成功但状态更新失败时必须返回错误：否则 offset 被提交，
// 数据库里这条消息会永远停在 PENDING/PROCESSED 之前的状态。
func TestHandleGenericMessage_StatusUpdateErrorIsReturned(t *testing.T) {
	repo := &stubMessageRepo{updateErr: errors.New("db down")}
	svc := &messageServiceImpl{repo: repo}

	msg := &mq.Message{
		Topic:   mq.TopicRecommendation,
		Value:   []byte(`{"message_id":"m-1"}`),
		Context: context.Background(),
	}
	if err := svc.handleGenericMessage(msg); err == nil {
		t.Fatal("状态更新失败应返回错误以触发重试")
	}
}
