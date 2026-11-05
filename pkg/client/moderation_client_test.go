package client

import (
	"context"
	"testing"

	pb "Logos/proto_gen/moderation"

	"google.golang.org/grpc"
)

type stubModerationClient struct {
	pb.ModerationServiceClient
	resp      *pb.ModerateContentResponse
	translate *pb.TranslateResponse
}

func (s stubModerationClient) ModerateContent(context.Context, *pb.ModerateContentRequest, ...grpc.CallOption) (*pb.ModerateContentResponse, error) {
	return s.resp, nil
}

func (s stubModerationClient) Translate(context.Context, *pb.TranslateRequest, ...grpc.CallOption) (*pb.TranslateResponse, error) {
	return s.translate, nil
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

// 翻译接口把失败编码在 Code 里，gRPC 层 err 仍是 nil。
// 客户端只取 translated_content 会把「模型不可用」读成「译文为空」的成功。
func TestModerationClient_TranslateNonZeroCodeReturnsError(t *testing.T) {
	c := NewModerationClient(stubModerationClient{
		translate: &pb.TranslateResponse{Code: 1, Message: "翻译服务不可用"},
	}, nil)

	got, err := c.Translate(context.Background(), "hello", "en", "zh", "c1")
	if err == nil {
		t.Fatalf("Code != 0 时应返回错误，实际 translated=%q", got)
	}
	if got != "" {
		t.Errorf("失败时不应返回译文，实际 %q", got)
	}
}

func TestModerationClient_TranslateZeroCodeReturnsContent(t *testing.T) {
	c := NewModerationClient(stubModerationClient{
		translate: &pb.TranslateResponse{Code: 0, TranslatedContent: "你好"},
	}, nil)

	got, err := c.Translate(context.Background(), "hello", "en", "zh", "c1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "你好" {
		t.Errorf("translated = %q, want 你好", got)
	}
}
