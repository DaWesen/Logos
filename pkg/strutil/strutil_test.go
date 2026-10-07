package strutil

import (
	"strings"
	"testing"
)

func TestCleanInvalidUTF8_RemovesInvalidBytes(t *testing.T) {
	valid := "你好，世界 hello"
	invalid := "bad\xff\xfeutf8"
	got := CleanInvalidUTF8(valid + invalid + "end")
	if strings.ContainsRune(got, 0xFFFD) {
		t.Errorf("result should not contain RuneError: %q", got)
	}
	if !strings.HasPrefix(got, valid) || !strings.HasSuffix(got, "end") {
		t.Errorf("valid parts should be preserved: %q", got)
	}
}

func TestCleanInvalidUTF8_RemovesNullBytes(t *testing.T) {
	got := CleanInvalidUTF8("a\x00b")
	if got != "ab" {
		t.Errorf("CleanInvalidUTF8(\"a\\x00b\") = %q, want \"ab\"", got)
	}
}

func TestCleanInvalidUTF8_ValidInputUnchanged(t *testing.T) {
	in := "plain ASCII + 中文 + emoji 😀"
	if got := CleanInvalidUTF8(in); got != in {
		t.Errorf("valid input should be unchanged, got %q", got)
	}
}

func TestTruncateByRunes_ShortInputUnchanged(t *testing.T) {
	in := "短文本"
	if got := TruncateByRunes(in, 100); got != in {
		t.Errorf("short input should be unchanged, got %q", got)
	}
}

func TestTruncateByRunes_TruncatesLongInput(t *testing.T) {
	in := strings.Repeat("字", 100)
	got := TruncateByRunes(in, 30)
	if got == in {
		t.Fatal("long input should be truncated")
	}
	// 头尾保留 + 截断标记
	if !strings.Contains(got, "…") && !strings.Contains(got, "...") {
		// 标记为 "…(内容已截断)" 或降级为 "..."
		if !strings.Contains(got, "内容已截断") {
			t.Errorf("truncation marker missing: %q", got)
		}
	}
}

func TestTruncateByRunes_NonPositiveLimit(t *testing.T) {
	if got := TruncateByRunes("hello", 0); got != "hello" {
		t.Errorf("maxRunes=0 should return original, got %q", got)
	}
	if got := TruncateByRunes("hello", -1); got != "hello" {
		t.Errorf("maxRunes=-1 should return original, got %q", got)
	}
}

func TestTruncateByRunes_TinyLimit(t *testing.T) {
	// 可用空间不足放置标记时应降级
	got := TruncateByRunes(strings.Repeat("ab", 50), 2)
	if len([]rune(got)) > 2+len("...(内容已截断)") {
		t.Errorf("tiny limit should produce short output, got %q", got)
	}
}
