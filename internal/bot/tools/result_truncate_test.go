package tools

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestTruncateMiddleOfToolResult_KeepsBothEnds 头尾都要保留：
// 结论常在末尾，只留头部会丢掉最有用的部分。
func TestTruncateMiddleOfToolResult_KeepsBothEnds(t *testing.T) {
	head := strings.Repeat("头", 4000)
	tail := "结论：共 42 条"
	s := head + tail

	got := truncateMiddleOfToolResult(s, 1000)
	if !strings.HasPrefix(got, "头头") {
		t.Fatalf("头部缺失: %q", got[:10])
	}
	if !strings.HasSuffix(got, tail) {
		t.Fatalf("尾部结论丢失: %q", got[len(got)-20:])
	}
	if !strings.Contains(got, "已省略") {
		t.Fatal("缺少截断标记，模型会把头尾误读为连续文本")
	}
	if n := utf8.RuneCountInString(got); n != 1000 {
		t.Fatalf("截断后长度 %d, want 1000", n)
	}
	if !utf8.ValidString(got) {
		t.Fatal("截断产生了非法 UTF-8")
	}
}

func TestTruncateMiddleOfToolResult_NoOpWithinLimit(t *testing.T) {
	s := "短结果"
	if got := truncateMiddleOfToolResult(s, 10); got != s {
		t.Fatalf("未超预算不应改动, got %q", got)
	}
}

func TestTruncateMiddleOfToolResult_TinyLimitFallsBackToHead(t *testing.T) {
	s := strings.Repeat("字", 100)
	got := truncateMiddleOfToolResult(s, 5)
	if n := utf8.RuneCountInString(got); n != 5 {
		t.Fatalf("小预算应退化为头部截断, len=%d", n)
	}
}

func TestTruncateMiddleOfToolResult_ZeroLimit(t *testing.T) {
	if got := truncateMiddleOfToolResult("内容", 0); got != "" {
		t.Fatalf("零预算应返回空, got %q", got)
	}
}
