package client

import (
	"context"
	"testing"

	pbCommon "Logos/proto_gen/common"
	pb "Logos/proto_gen/question"

	"google.golang.org/grpc"
)

type stubQAClient struct {
	pb.QAServiceClient
	req      *pb.QuestionReq
	resp     *pbCommon.BaseResp
	degraded bool
	err      error
}

func (s *stubQAClient) AskQuestion(_ context.Context, req *pb.QuestionReq, _ ...grpc.CallOption) (*pb.AnswerResp, error) {
	s.req = req
	return &pb.AnswerResp{Answer: "ok", QuestionId: "q-1", Degraded: s.degraded}, nil
}

func (s *stubQAClient) GetRecommendedQuestions(
	_ context.Context,
	_ *pb.GetRecommendedQuestionsReq,
	_ ...grpc.CallOption,
) (*pbCommon.BaseResp, error) {
	return s.resp, s.err
}

func TestQuestionClient_GetRecommendedQuestionsParsesEnvelope(t *testing.T) {
	client := NewQuestionClient(&stubQAClient{
		resp: &pbCommon.BaseResp{
			StatusCode:    200,
			StatusMessage: `{"questions":["什么是 RAG？","如何做限流？"]}`,
		},
	}, nil)

	got, err := client.GetRecommendedQuestions(context.Background(), 7)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 || got[0] != "什么是 RAG？" {
		t.Fatalf("推荐问题解析错误: %v", got)
	}
}

// 下游把结果包在 {"questions": [...]} 里；按裸数组解析会恒定得到空切片，
// 且错误被吞后，调用方完全看不出是模型没生成还是响应格式变了。
func TestQuestionClient_GetRecommendedQuestionsRejectsBadJSON(t *testing.T) {
	client := NewQuestionClient(&stubQAClient{
		resp: &pbCommon.BaseResp{StatusCode: 200, StatusMessage: "not-json"},
	}, nil)

	if _, err := client.GetRecommendedQuestions(context.Background(), 7); err == nil {
		t.Fatal("响应格式错误必须返回 error，不能伪装成空推荐")
	}
}

func TestQuestionClient_GetRecommendedQuestionsEmptyMessage(t *testing.T) {
	client := NewQuestionClient(&stubQAClient{
		resp: &pbCommon.BaseResp{StatusCode: 200},
	}, nil)

	got, err := client.GetRecommendedQuestions(context.Background(), 7)
	if err != nil {
		t.Fatalf("空响应不应报错: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("空响应应返回空列表，got %v", got)
	}
}

func TestQuestionClient_AskQuestionWithRequestIDPropagatesKey(t *testing.T) {
	stub := &stubQAClient{}
	c := NewQuestionClient(stub, nil)

	if _, err := c.AskQuestionWithRequestID(context.Background(), "hello", 7, "m-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stub.req.GetRequestId() != "m-1" {
		t.Fatalf("request_id = %q, want m-1", stub.req.GetRequestId())
	}
	if stub.req.GetUserId() != 7 || stub.req.GetContent() != "hello" {
		t.Fatalf("请求字段未透传: %+v", stub.req)
	}
}

// 不带幂等键的调用必须显式发送空 request_id，服务端据此保持 UUID 行为。
func TestQuestionClient_AskQuestionKeepsEmptyRequestID(t *testing.T) {
	stub := &stubQAClient{}
	c := NewQuestionClient(stub, nil)

	if _, err := c.AskQuestion(context.Background(), "hello", 7); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stub.req.GetRequestId() != "" {
		t.Fatalf("request_id = %q, want empty", stub.req.GetRequestId())
	}
}

// 只返回 answer 会丢掉降级状态：调用方无法判断模型是否真的回答过，
// 消费端就会把兜底文案当成成功消息提交 offset。
func TestQuestionClient_AskQuestionExposesDegradedFlag(t *testing.T) {
	stub := &stubQAClient{degraded: true}
	c := NewQuestionClient(stub, nil)

	result, err := c.AskQuestionWithRequestID(context.Background(), "hello", 7, "m-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Degraded {
		t.Fatal("降级标记必须透传给调用方")
	}
	if result.QuestionID != "q-1" || result.Answer != "ok" {
		t.Fatalf("响应字段未透传: %+v", result)
	}
}
