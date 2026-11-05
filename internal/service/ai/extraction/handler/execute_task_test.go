package handler

import (
	"context"
	"errors"
	"testing"

	"Logos/internal/service/ai/extraction/model"
	"Logos/internal/service/ai/extraction/service"
	pb "Logos/proto_gen/extraction"
)

// stubExtractionService 只覆盖被测方法，其余方法由嵌入的 nil 接口兜底。
type stubExtractionService struct {
	service.ExtractionService

	result *model.ExtractionResult
	err    error
}

func (s stubExtractionService) ExecuteTask(ctx context.Context, taskID string) (*model.ExtractionResult, error) {
	return s.result, s.err
}

func TestExecuteTask_ErrorWithoutResultDoesNotPanic(t *testing.T) {
	// 旧实现无论成败都调用 resultToProto：任务被并发抢占/限流时
	// result 为 nil，转 proto 会在方法集调用处直接 panic。
	handler := &KnowledgeExtractionServiceImpl{
		ExtractionService: stubExtractionService{
			result: nil,
			err:    errors.New("任务正在执行中"),
		},
	}

	resp, err := handler.ExecuteTask(context.Background(), &pb.ExecuteExtractionTaskReq{TaskId: "t-1"})
	if err != nil {
		t.Fatalf("handler 不应把业务错误变成 gRPC error: %v", err)
	}
	if resp.BaseResp.StatusCode == 0 {
		t.Fatal("执行失败时状态码不应为 0")
	}
	if resp.BaseResp.StatusMessage != "任务正在执行中" {
		t.Fatalf("应透传原始错误，实际 %q", resp.BaseResp.StatusMessage)
	}
	if resp.Result != nil {
		t.Fatalf("失败时不应返回结果，实际 %#v", resp.Result)
	}
}

func TestExecuteTask_NilResultWithoutErrorIsError(t *testing.T) {
	handler := &KnowledgeExtractionServiceImpl{
		ExtractionService: stubExtractionService{},
	}

	resp, err := handler.ExecuteTask(context.Background(), &pb.ExecuteExtractionTaskReq{TaskId: "t-1"})
	if err != nil {
		t.Fatalf("handler 不应把业务错误变成 gRPC error: %v", err)
	}
	if resp.BaseResp.StatusCode != 500 {
		t.Fatalf("空结果应返回 500，实际 %d", resp.BaseResp.StatusCode)
	}
}
