package service

import (
	"context"
	"testing"

	"Logos/pkg/logger"

	"go.uber.org/zap"
)

// 审核模型不可用时必须失败关闭：返回错误让调用方拒绝内容，而不是默认 passed。
func TestModerateContent_FailsClosedWithoutChatModel(t *testing.T) {
	logger.SetLogger(zap.NewNop())

	svc := NewModerationService(nil, nil)
	record, err := svc.ModerateContent(context.Background(), "hello", "content-1", "text", nil)
	if err == nil {
		t.Fatalf("审核模型不可用时应返回错误，实际返回 record=%+v", record)
	}
	if record != nil {
		t.Errorf("失败时不应返回审核记录，实际为 %+v", record)
	}
}
