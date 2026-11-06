package service

import (
	"context"
	"errors"
	"testing"

	"Logos/pkg/client"
	pb "Logos/proto_gen/question"

	"google.golang.org/grpc"
)

type recordingQAClient struct {
	pb.QAServiceClient
	req      *pb.QuestionReq
	degraded bool
	err      error
}

func (c *recordingQAClient) AskQuestion(
	_ context.Context,
	req *pb.QuestionReq,
	_ ...grpc.CallOption,
) (*pb.AnswerResp, error) {
	c.req = req
	if c.err != nil {
		return nil, c.err
	}
	return &pb.AnswerResp{Answer: "ok", QuestionId: "q-1", Degraded: c.degraded}, nil
}

func TestQuestionClientAdapter_PropagatesUserAndRequestID(t *testing.T) {
	stub := &recordingQAClient{}
	adapter := NewQuestionClientAdapter(client.NewQuestionClient(stub, nil))
	userID := "42"

	if _, err := adapter.AskQuestion(context.Background(), "hello", &userID, "m-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stub.req.GetUserId() != 42 {
		t.Fatalf("user_id = %d, want 42", stub.req.GetUserId())
	}
	if stub.req.GetRequestId() != "m-1" {
		t.Fatalf("request_id = %q, want m-1", stub.req.GetRequestId())
	}
	if stub.req.GetContent() != "hello" {
		t.Fatalf("content = %q, want hello", stub.req.GetContent())
	}
}

// 旧实现遇到非空 userID 会直接返回 nil,nil，调用方以为问答成功但模型根本没被调用。
func TestQuestionClientAdapter_RejectsInvalidUserID(t *testing.T) {
	stub := &recordingQAClient{}
	adapter := NewQuestionClientAdapter(client.NewQuestionClient(stub, nil))
	userID := "not-a-number"

	if _, err := adapter.AskQuestion(context.Background(), "hello", &userID, "m-1"); err == nil {
		t.Fatal("非法 user_id 必须返回错误，不能静默跳过问答")
	}
	if stub.req != nil {
		t.Fatal("非法 user_id 不应发起下游调用")
	}
}

func TestQuestionClientAdapter_NilUserIDKeepsAnonymousRequest(t *testing.T) {
	stub := &recordingQAClient{}
	adapter := NewQuestionClientAdapter(client.NewQuestionClient(stub, nil))

	if _, err := adapter.AskQuestion(context.Background(), "hello", nil, "m-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stub.req.GetUserId() != 0 {
		t.Fatalf("匿名请求 user_id = %d, want 0", stub.req.GetUserId())
	}
	if stub.req.GetRequestId() != "m-1" {
		t.Fatalf("匿名请求仍应透传 request_id, got %q", stub.req.GetRequestId())
	}
}

// 降级答案不是成功：适配器必须返回错误，Kafka 才会重试而不是提交 offset。
// 否则用户看到的「正在准备中」永远不会再被真正的回答替换。
func TestQuestionClientAdapter_TreatsDegradedAnswerAsFailure(t *testing.T) {
	stub := &recordingQAClient{degraded: true}
	adapter := NewQuestionClientAdapter(client.NewQuestionClient(stub, nil))

	_, err := adapter.AskQuestion(context.Background(), "hello", nil, "m-1")
	if !errors.Is(err, ErrDegradedAnswer) {
		t.Fatalf("降级答案应返回 ErrDegradedAnswer, got %v", err)
	}
}
