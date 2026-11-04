package eino

import (
	"os"
	"testing"

	"Logos/pkg/logger"

	"go.uber.org/zap"
)

// TestMain 注入 nop logger：eino 包会打日志，默认 logger 初始化依赖 config 文件，
// 单测目录下没有配置，会在第一次打日志时 panic。
func TestMain(m *testing.M) {
	logger.SetLogger(zap.NewNop())
	os.Exit(m.Run())
}
