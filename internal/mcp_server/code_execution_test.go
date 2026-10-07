package mcp_server

import (
	"strings"
	"testing"
)

func TestSanitizeEnv_Whitelist(t *testing.T) {
	environ := []string{
		"PATH=/usr/bin",
		"HOME=/root",
		"ARK_API_KEY=sk-secret",
		"JWT_SECRET=super-secret",
		"OPENAI_API_KEY=sk-leak",
		"LANG=en_US.UTF-8",
	}
	got := sanitizeEnv(environ)

	joined := strings.Join(got, "\n")
	for _, secret := range []string{"sk-secret", "super-secret", "sk-leak", "ARK_API_KEY", "JWT_SECRET", "OPENAI_API_KEY"} {
		if strings.Contains(joined, secret) {
			t.Errorf("sanitizeEnv leaked %q: %v", secret, got)
		}
	}
	for _, want := range []string{"PATH=/usr/bin", "HOME=/root", "LANG=en_US.UTF-8"} {
		if !strings.Contains(joined, want) {
			t.Errorf("sanitizeEnv should keep %q, got %v", want, got)
		}
	}
}

func TestSanitizeEnv_LowercaseKeys(t *testing.T) {
	// Windows 环境变量不区分大小写：path= 也应被保留
	got := sanitizeEnv([]string{"path=/bin", "ARK_API_KEY=secret"})
	if len(got) != 1 || got[0] != "path=/bin" {
		t.Errorf("case-insensitive whitelist failed: %v", got)
	}
}

func TestSanitizeEnv_Empty(t *testing.T) {
	if got := sanitizeEnv(nil); len(got) != 0 {
		t.Errorf("nil environ should return empty, got %v", got)
	}
}

func TestClampTimeout(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{"", defaultTimeoutSecs},
		{"10", 10},
		{"60", 60},
		{"3600", maxTimeoutSeconds}, // 超上限被钳制
		{"999999", maxTimeoutSeconds},
		{"0", defaultTimeoutSecs},   // 非法值回退默认
		{"-5", defaultTimeoutSecs},
		{"abc", defaultTimeoutSecs},
	}
	for _, tt := range tests {
		if got := clampTimeout(tt.in); got != tt.want {
			t.Errorf("clampTimeout(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestTruncateOutput(t *testing.T) {
	short := "hello"
	if got := truncateOutput(short); got != short {
		t.Errorf("short output should be unchanged, got %q", got)
	}

	long := strings.Repeat("x", maxOutputSize+1000)
	got := truncateOutput(long)
	if len(got) >= len(long) {
		t.Error("long output should be truncated")
	}
	if !strings.Contains(got, "已截断") {
		t.Error("truncated output should carry a marker")
	}
}

func TestLimitedBuffer_DropsBeyondMax(t *testing.T) {
	b := &limitedBuffer{max: 10}
	n, _ := b.Write([]byte("0123456789"))
	if n != 10 {
		t.Errorf("first write returned %d, want 10", n)
	}
	// 超限写入被丢弃
	n, _ = b.Write([]byte("overflow"))
	if n != 8 {
		t.Errorf("overflow write should report len(p)=%d, got %d", 8, n)
	}
	if b.String() != "0123456789" {
		t.Errorf("buffer should keep first 10 bytes, got %q", b.String())
	}
}

func TestLimitedBuffer_PartialWriteAtBoundary(t *testing.T) {
	b := &limitedBuffer{max: 10}
	// 一次写入 15 字节，只保留前 10
	if _, err := b.Write([]byte("aaaaaaaaaaaaaaa")); err != nil {
		t.Fatalf("write error: %v", err)
	}
	if b.String() != "aaaaaaaaaa" {
		t.Errorf("expected first 10 bytes kept, got %q", b.String())
	}
}

func TestDockerImageFor(t *testing.T) {
	tests := []struct {
		lang       string
		wantImage  string
		wantFile   string
	}{
		{"python", "python:3.11-slim", "main.py"},
		{"PYTHON", "python:3.11-slim", "main.py"},
		{"javascript", "node:20-alpine", "main.js"},
		{"node", "node:20-alpine", "main.js"},
		{"go", "golang:1.25-alpine", "main.go"},
		{"unknown", "python:3.11-slim", "main.py"}, // 未知语言回退默认
	}
	for _, tt := range tests {
		img, file := dockerImageFor(tt.lang)
		if img != tt.wantImage || file != tt.wantFile {
			t.Errorf("dockerImageFor(%q) = (%q, %q), want (%q, %q)", tt.lang, img, file, tt.wantImage, tt.wantFile)
		}
	}
}

func TestCodeExecutionTool_ParametersAndMetadata(t *testing.T) {
	tool := &CodeExecutionTool{}
	if tool.Name() != "code_execution" {
		t.Errorf("unexpected name %q", tool.Name())
	}
	params := tool.Parameters()
	if len(params) != 3 {
		t.Errorf("expected 3 params, got %d", len(params))
	}

	// 缺少 code 参数
	res, err := tool.Execute(t.Context(), map[string]string{})
	if err != nil || !res.IsError {
		t.Errorf("missing code should return error result: %v, %+v", err, res)
	}

	// 代码超限
	res, err = tool.Execute(t.Context(), map[string]string{
		"code": strings.Repeat("a", maxCodeSize+1),
		"language": "python",
	})
	if err != nil || !res.IsError {
		t.Errorf("oversized code should return error result: %v, %+v", err, res)
	}

	// 不支持的语言（不实际执行任何解释器，只做路由错误）
	// executeCode 会因未知语言在写文件前返回错误
	res, err = tool.Execute(t.Context(), map[string]string{
		"code": "print(1)",
		"language": "cobol",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError {
		t.Errorf("unsupported language should be an error result: %+v", res)
	}
}
