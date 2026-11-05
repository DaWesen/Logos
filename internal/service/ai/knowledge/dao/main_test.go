package dao

import (
	"os"
	"testing"

	"Logos/pkg/logger"

	"go.uber.org/zap"
)

// TestMain 注入 nop logger，让图补全的降级日志不依赖配置文件。
func TestMain(m *testing.M) {
	logger.SetLogger(zap.NewNop())
	os.Exit(m.Run())
}
