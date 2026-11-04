package service

import (
	"context"
	"errors"
	"testing"
	"time"

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
