package service

import (
	"os"
	"testing"

	"Logos/pkg/logger"

	"go.uber.org/zap"
)

// TestMain 注入 nop logger，隔离测试对配置文件的依赖。
// 推荐服务在入口打 Info 日志，默认 logger 会尝试读 config 并 panic。
func TestMain(m *testing.M) {
	logger.SetLogger(zap.NewNop())
	os.Exit(m.Run())
}
