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
	resp *pbCommon.BaseResp
	err  error
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
