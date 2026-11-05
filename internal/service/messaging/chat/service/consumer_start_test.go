package service

import (
	"context"
	"strings"
	"testing"
)

// TestStartEventConsumer_NilEventBusFailsFast 验证依赖缺失时启动直接失败。
// 修复前订阅在裸 goroutine 中执行，nil EventBus 引发的 panic 会击穿进程；
// 即便换成普通订阅错误，日志之外也没有任何调用方能看到启动失败。
func TestStartEventConsumer_NilEventBusFailsFast(t *testing.T) {
	svc := &ChatServiceImpl{ctx: context.Background()}

	err := svc.StartEventConsumer()
	if err == nil {
		t.Fatal("event bus 为 nil 时应返回启动错误")
	}
	if !strings.Contains(err.Error(), "event bus") {
		t.Fatalf("错误应说明 event bus 未初始化，实际: %v", err)
	}
}
