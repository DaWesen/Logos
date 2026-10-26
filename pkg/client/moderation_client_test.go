package client

import (
	"context"
	"testing"

	pb "Logos/proto_gen/moderation"

	"google.golang.org/grpc"
)

type stubModerationClient struct {
	pb.ModerationServiceClient
	resp *pb.ModerateContentResponse
}

func (s stubModerationClient) ModerateContent(context.Context, *pb.ModerateContentRequest, ...grpc.CallOption) (*pb.ModerateContentResponse, error) {
	return s.resp, nil
}

// 审核服务返回 Code != 0 时必须向上抛错，不能被当成“通过”。
func TestModerationClient_NonZeroCodeReturnsError(t *testing.T) {
	c := NewModerationClient(stubModerationClient{
		resp: &pb.ModerateContentResponse{Code: 1, Message: "审核服务不可用"},
	}, nil)

	if _, err := c.ModerateContent(context.Background(), "hello", "c1", "text"); err == nil {
		t.Fatal("Code != 0 时应返回错误")
	}
}

func TestModerationClient_ZeroCodeReturnsResult(t *testing.T) {
	c := NewModerationClient(stubModerationClient{
		resp: &pb.ModerateContentResponse{
			Code: 0,
			Data: &pb.ModerationRecord{Result: pb.ModerationResult_MODERATION_RESULT_REJECTED},
		},
	}, nil)

	got, err := c.ModerateContent(context.Background(), "hello", "c1", "text")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Result != int32(pb.ModerationResult_MODERATION_RESULT_REJECTED) {
		t.Errorf("Result = %d, want rejected", got.Result)
	}
}
