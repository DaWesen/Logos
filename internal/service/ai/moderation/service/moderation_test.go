package service

import (
	"context"
	"strings"
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

// 翻译同理：模型不可用时不能把一句占位文案当成译文返回，
// 否则调用方会把它展示给用户、写进记录，且无法与真正的译文区分。
func TestTranslate_FailsClosedWithoutChatModel(t *testing.T) {
	logger.SetLogger(zap.NewNop())

	svc := NewModerationService(nil, nil)
	record, err := svc.Translate(context.Background(), "hello", "en", "zh", "content-1", nil)
	if err == nil {
		t.Fatalf("翻译模型不可用时应返回错误，实际返回 record=%+v", record)
	}
	if record != nil {
		t.Errorf("失败时不应返回翻译记录，实际为 %+v", record)
	}
	if strings.Contains(err.Error(), "暂不可用") {
		t.Errorf("错误信息不应伪装成译文占位符: %v", err)
	}
}
