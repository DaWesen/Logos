package service

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSplitTextIntoChunks_EmptyInput(t *testing.T) {
	if got := SplitTextIntoChunks("", 1000, 100); got != nil {
		t.Errorf("empty input should return nil, got %v", got)
	}
	if got := SplitTextIntoChunks("   \n  \n", 1000, 100); got != nil {
		t.Errorf("whitespace-only input should return nil, got %v", got)
	}
}

func TestSplitTextIntoChunks_SplitsByHeadings(t *testing.T) {
	// 两个超限小节（多行）→ 各自按标题边界成块，节内降级窗口切分
	sectionA := "# 第一章\n" + strings.Repeat("章一内容行。\n", 60) // ~1150 字节
	sectionB := "# 第二章\n" + strings.Repeat("章二内容行。\n", 60)
	doc := sectionA + "\n" + sectionB

	chunks := SplitTextIntoChunks(doc, 1000, 100)
	if len(chunks) < 3 {
		t.Fatalf("expected at least 3 chunks (two sections + window splits), got %d", len(chunks))
	}

	// 每节首块以标题开头（结构感知：标题保留在块内）
	if !strings.HasPrefix(chunks[0], "# 第一章") {
		t.Errorf("first chunk should start with heading, got %q", chunks[0][:min(20, len(chunks[0]))])
	}
	foundSecond := false
	for _, c := range chunks {
		if strings.HasPrefix(c, "# 第二章") {
			foundSecond = true
			break
		}
	}
	if !foundSecond {
		t.Error("a chunk should start with '# 第二章'")
	}

	for i, c := range chunks {
		if len(c) > 1100 {
			t.Errorf("chunk %d exceeds size limit: %d", i, len(c))
		}
	}

	// 内容不丢失
	joined := strings.Join(chunks, "\n")
	for _, want := range []string{"章一内容行", "章二内容行"} {
		if !strings.Contains(joined, want) {
			t.Errorf("content %q lost after chunking", want)
		}
	}
	// 第二章内容不应混入第一块
	if strings.Contains(chunks[0], "章二内容行") {
		t.Error("sections should not be mixed in the same chunk when total size exceeds limit")
	}
}

func TestSplitTextIntoChunks_OverlongLineIsHardSplit(t *testing.T) {
	// 无换行的超长行：按 rune 边界硬切，不产生非法 UTF-8
	long := strings.Repeat("章一内容", 300) // 单行 ~3600 字节
	chunks := SplitTextIntoChunks(long, 1000, 100)
	if len(chunks) < 3 {
		t.Fatalf("overlong single line should be hard split, got %d chunks", len(chunks))
	}
	for i, c := range chunks {
		if len(c) > 1010 {
			t.Errorf("chunk %d too large: %d", i, len(c))
		}
		if !utf8.ValidString(c) {
			t.Errorf("chunk %d contains invalid UTF-8 (rune boundary violated)", i)
		}
	}
}

func TestSplitTextIntoChunks_MergesShortSections(t *testing.T) {
	// 两个短小节应合并为一个块
	doc := "# A\nshort a\n\n# B\nshort b\n"
	chunks := SplitTextIntoChunks(doc, 1000, 100)
	if len(chunks) != 1 {
		t.Fatalf("short adjacent sections should be merged into one chunk, got %d: %v", len(chunks), chunks)
	}
	if !strings.Contains(chunks[0], "short a") || !strings.Contains(chunks[0], "short b") {
		t.Errorf("merged chunk should contain both sections, got %q", chunks[0])
	}
}

func TestSplitTextIntoChunks_LongSectionFallsBackToWindow(t *testing.T) {
	// 单个超长无标题小节（多行）：降级为窗口切分
	var sb strings.Builder
	for i := 0; i < 200; i++ {
		sb.WriteString("段落内容行，每行约十个字节。\n")
	}
	long := sb.String() // ~2400 字节

	chunks := SplitTextIntoChunks(long, 1000, 100)
	if len(chunks) < 2 {
		t.Fatalf("long section should be split into multiple chunks, got %d", len(chunks))
	}
	for i, c := range chunks {
		if len(c) > 1100 { // 1000 + 容忍少量行边界溢出
			t.Errorf("chunk %d too large: %d", i, len(c))
		}
	}
	// 相邻块间有重叠（尾部内容出现在下一块中）
	if len(chunks) >= 2 && len(chunks[0]) > 100 {
		tail := chunks[0][len(chunks[0])-100:]
		if !strings.Contains(chunks[1], tail) {
			t.Error("adjacent chunks should overlap for context continuity")
		}
	}
}

func TestSplitTextIntoChunks_PlainTextWithoutHeadings(t *testing.T) {
	// 无结构文本：整体作为单节处理，超长时窗口切分
	text := strings.Repeat("hello world line\n", 100) // ~1700 字节
	chunks := SplitTextIntoChunks(text, 1000, 100)
	if len(chunks) < 2 {
		t.Fatalf("long plain text should be split, got %d", len(chunks))
	}
}

func TestSplitTextIntoChunks_DefaultParams(t *testing.T) {
	// 非法参数回退默认值（多行文本，避免单行超长不可切）
	var sb strings.Builder
	for i := 0; i < 40; i++ {
		sb.WriteString(strings.Repeat("x", 40) + "\n")
	}
	chunks := SplitTextIntoChunks(sb.String(), 0, -1)
	if len(chunks) < 2 {
		t.Fatalf("expected default chunk size to split ~1640 bytes, got %d chunks", len(chunks))
	}
}

func TestIsHeading(t *testing.T) {
	tests := []struct {
		line string
		want bool
	}{
		{"# 标题", true},
		{"###### 六级标题", true},
		{"##二级", false}, // # 后必须有空格
		{"####### 七个#", false},
		{"普通文本", false},
		{"  # 缩进标题", true},
		{"", false},
	}
	for _, tt := range tests {
		if got := isHeading(tt.line); got != tt.want {
			t.Errorf("isHeading(%q) = %v, want %v", tt.line, got, tt.want)
		}
	}
}
