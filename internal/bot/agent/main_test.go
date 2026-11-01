package agent

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestMain 指向仓库内的最小配置夹具，避免测试隐式依赖开发者本地的 config/config.yaml
// （该文件被 .gitignore 排除，CI 上并不存在，logger 初始化会直接 panic）。
func TestMain(m *testing.M) {
	_, file, _, _ := runtime.Caller(0)
	fixture := filepath.Join(filepath.Dir(file), "..", "..", "..", "config", "testdata", "config.yaml")
	if abs, err := filepath.Abs(fixture); err == nil {
		fixture = abs
	}
	os.Setenv("LOGOS_CONFIG_PATH", fixture)
	os.Exit(m.Run())
}
