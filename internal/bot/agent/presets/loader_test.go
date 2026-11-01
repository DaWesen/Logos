package presets

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"Logos/internal/bot/agent"
)

// TestMain 指向仓库内的最小配置夹具：logger 初始化会读 config，
// 而 config/config.yaml 被 .gitignore 排除、CI 上不存在，否则直接 panic。
func TestMain(m *testing.M) {
	_, file, _, _ := runtime.Caller(0)
	fixture := filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "config", "testdata", "config.yaml")
	if abs, err := filepath.Abs(fixture); err == nil {
		fixture = abs
	}
	os.Setenv("LOGOS_CONFIG_PATH", fixture)
	os.Exit(m.Run())
}

// TestParsePresets_Filters 空名、未启用、重复名都不应进入注册列表。
func TestParsePresets_Filters(t *testing.T) {
	data := []byte(`
bots:
  - name: "A"
    enabled: true
  - name: "A"
    enabled: true
  - name: "  "
    enabled: true
  - name: "B"
    enabled: false
  - name: "C"
    enabled: true
`)

	got, err := parsePresets(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("应得到 A/C 两条，got %d: %+v", len(got), got)
	}
	if got[0].Name != "A" || got[1].Name != "C" {
		t.Fatalf("got %+v", got)
	}
}

// TestParsePresets_MalformedReturnsError YAML 语法错误必须返回 error，
// 而不是在 loader 里递归调用 RegisterAllPresets（旧实现会栈溢出）。
func TestParsePresets_MalformedReturnsError(t *testing.T) {
	if _, err := parsePresets([]byte("bots: [\n  - name: A\n")); err == nil {
		t.Fatal("非法 YAML 必须返回 error")
	}
}

// TestLoadPresetsFromYAML_MalformedReturnsError 走完整入口验证不再递归：
// 旧实现会在这里转去 RegisterAllPresets → 再读默认路径 → 再次失败 → 无限递归。
func TestLoadPresetsFromYAML_MalformedReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "broken.yaml")
	if err := os.WriteFile(path, []byte("bots: [\n"), 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	mgr := agent.NewAgentManager(nil)
	if err := LoadPresetsFromYAML(mgr, path); err == nil {
		t.Fatal("非法 YAML 应返回 error，交由上层回退")
	}
}
