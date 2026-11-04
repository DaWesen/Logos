package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
)

// writeConfig 写一份最小配置并返回路径。
// viper 是包级单例，测试之间必须 Reset，否则上一个用例的配置会被复用。
func writeConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadConfig_EnvOverridesMinioBucket(t *testing.T) {
	path := writeConfig(t, `
minio:
  endpoint: localhost:9000
  bucket: default-bucket
`)
	viper.Reset()
	t.Setenv("LOGOS_CONFIG_PATH", path)
	t.Setenv("MINIO_BUCKET", "logos-media")

	cfg, err := LoadConfig(filepath.Dir(path))
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	if cfg.Minio.Bucket != "logos-media" {
		t.Fatalf("MINIO_BUCKET 未生效: %q", cfg.Minio.Bucket)
	}
}

func TestLoadConfig_EnvOverridesQQBridgeTempDir(t *testing.T) {
	path := writeConfig(t, `
qqbridge:
  temp_dir: /tmp/qqbridge-default
`)
	want := filepath.Join(t.TempDir(), "qqbridge")
	viper.Reset()
	t.Setenv("LOGOS_CONFIG_PATH", path)
	t.Setenv("QQBRIDGE_TEMP_DIR", want)

	cfg, err := LoadConfig(filepath.Dir(path))
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	if cfg.QQBridge.TempDir != want {
		t.Fatalf("QQBRIDGE_TEMP_DIR 未生效: %q", cfg.QQBridge.TempDir)
	}
}

func TestLoadConfig_EnvEmptyKeepsFileValue(t *testing.T) {
	path := writeConfig(t, `
minio:
  bucket: from-file
`)
	viper.Reset()
	t.Setenv("LOGOS_CONFIG_PATH", path)
	t.Setenv("MINIO_BUCKET", "")

	cfg, err := LoadConfig(filepath.Dir(path))
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	if cfg.Minio.Bucket != "from-file" {
		t.Fatalf("环境变量为空时应保留配置文件值，got %q", cfg.Minio.Bucket)
	}
}

func TestLoadConfig_EnvOverridesQQBridgeLimits(t *testing.T) {
	path := writeConfig(t, `
qqbridge:
  fast_concurrency: 2
  media_concurrency: 4
  media_max_bytes_mb: 50
  media_timeout_seconds: 30
`)
	viper.Reset()
	t.Setenv("LOGOS_CONFIG_PATH", path)
	t.Setenv("QQBRIDGE_FAST_CONCURRENCY", "16")
	t.Setenv("QQBRIDGE_MEDIA_CONCURRENCY", "12")
	t.Setenv("QQBRIDGE_MEDIA_MAX_MB", "200")
	t.Setenv("QQBRIDGE_MEDIA_TIMEOUT_SECONDS", "120")

	cfg, err := LoadConfig(filepath.Dir(path))
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	if cfg.QQBridge.FastConcurrency != 16 {
		t.Fatalf("QQBRIDGE_FAST_CONCURRENCY 未生效: %d", cfg.QQBridge.FastConcurrency)
	}
	if cfg.QQBridge.MediaConcurrency != 12 {
		t.Fatalf("QQBRIDGE_MEDIA_CONCURRENCY 未生效: %d", cfg.QQBridge.MediaConcurrency)
	}
	if cfg.QQBridge.MediaMaxBytesMB != 200 {
		t.Fatalf("QQBRIDGE_MEDIA_MAX_MB 未生效: %d", cfg.QQBridge.MediaMaxBytesMB)
	}
	if cfg.QQBridge.MediaTimeoutSeconds != 120 {
		t.Fatalf("QQBRIDGE_MEDIA_TIMEOUT_SECONDS 未生效: %d", cfg.QQBridge.MediaTimeoutSeconds)
	}
}

func TestLoadConfig_EnvInvalidNumberKeepsFileValue(t *testing.T) {
	// 环境变量写错（如 "abc"）不能把合法配置清成 0，
	// 否则并发会悄悄退化成默认值，运维却以为改成了 16。
	path := writeConfig(t, `
qqbridge:
  fast_concurrency: 6
`)
	viper.Reset()
	t.Setenv("LOGOS_CONFIG_PATH", path)
	t.Setenv("QQBRIDGE_FAST_CONCURRENCY", "not-a-number")

	cfg, err := LoadConfig(filepath.Dir(path))
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	if cfg.QQBridge.FastConcurrency != 6 {
		t.Fatalf("非法环境变量应保留配置文件值，got %d", cfg.QQBridge.FastConcurrency)
	}
}
