package parser

import (
	"context"
	"io"
	"strings"
	"testing"
)

func newTestManager() *ParserManager {
	return NewParserManager(nil, nil, nil)
}

func TestParserManager_RoutesByExtension(t *testing.T) {
	tests := []struct {
		fileType string
		wantType string // 期望解析器类型（nil 表示不应有解析器）
		wantNil  bool
	}{
		{"txt", "*parser.TextParser", false},
		{"md", "*parser.TextParser", false},
		{"json", "*parser.TextParser", false},
		{"jpg", "*parser.ImageParser", false},
		{"png", "*parser.ImageParser", false},
		{"mp3", "*parser.AudioParser", false},
		{"wav", "*parser.AudioParser", false},
		{"mp4", "*parser.VideoParser", false},
		{"pdf", "*parser.DocParser", false},
		{"docx", "*parser.DocParser", false},
		{"url", "*parser.CrawlerParser", false},
		{"webpage", "*parser.CrawlerParser", false},
		{"*", "*parser.GenericParser", false},
		{"unknown-ext", "", true}, // 未注册且非 * 的类型返回 nil
	}

	pm := newTestManager()
	for _, tt := range tests {
		got := pm.GetParser(tt.fileType)
		if tt.wantNil {
			if got != nil {
				t.Errorf("GetParser(%q) = %T, want nil", tt.fileType, got)
			}
			continue
		}
		if got == nil {
			t.Errorf("GetParser(%q) = nil, want %s", tt.fileType, tt.wantType)
			continue
		}
		if gotType := typeName(got); gotType != tt.wantType {
			t.Errorf("GetParser(%q) = %s, want %s", tt.fileType, gotType, tt.wantType)
		}
	}
}

func typeName(p DocumentParser) string {
	switch p.(type) {
	case *TextParser:
		return "*parser.TextParser"
	case *ImageParser:
		return "*parser.ImageParser"
	case *AudioParser:
		return "*parser.AudioParser"
	case *VideoParser:
		return "*parser.VideoParser"
	case *DocParser:
		return "*parser.DocParser"
	case *CrawlerParser:
		return "*parser.CrawlerParser"
	case *GenericParser:
		return "*parser.GenericParser"
	default:
		return "unknown"
	}
}

func TestParserManager_HasParser(t *testing.T) {
	pm := newTestManager()
	if !pm.HasParser("txt") {
		t.Error("txt should have a parser")
	}
	if pm.HasParser("xyz123") {
		t.Error("unregistered type should not have a parser")
	}
	if !pm.HasParser("*") {
		t.Error("* should have a parser (generic fallback)")
	}
}

// fakeParser 用于自定义注册测试
type fakeParser struct{}

func (f *fakeParser) Parse(ctx context.Context, reader io.Reader, filename string) (string, map[string]interface{}, error) {
	return "fake", nil, nil
}

func (f *fakeParser) SupportedTypes() []string {
	return []string{"fake"}
}

func TestParserManager_RegisterCustomParser(t *testing.T) {
	pm := newTestManager()
	pm.RegisterParser("fake", &fakeParser{})

	if !pm.HasParser("fake") {
		t.Fatal("custom parser should be registered")
	}
	p := pm.GetParser("fake")
	if _, ok := p.(*fakeParser); !ok {
		t.Fatalf("GetParser(fake) = %T, want *fakeParser", p)
	}
}

func TestTextParser_Parse(t *testing.T) {
	p := NewTextParser()
	content := "line1\nline2 line3\n"
	text, meta, err := p.Parse(context.Background(), strings.NewReader(content), "test.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != content {
		t.Errorf("text = %q, want %q", text, content)
	}
	if meta == nil {
		t.Fatal("meta should not be nil")
	}
	if meta["file_name"] != "test.txt" {
		t.Errorf("meta[file_name] = %v, want test.txt", meta["file_name"])
	}
	if meta["lines"] != 2 {
		t.Errorf("meta[lines] = %v, want 2", meta["lines"])
	}
	if meta["words"] != 3 {
		t.Errorf("meta[words] = %v, want 3", meta["words"])
	}
}

func TestGetFileType(t *testing.T) {
	tests := []struct {
		filename string
		want     string
	}{
		{"doc.PDF", "pdf"},
		{"a.tar.gz", "gz"},
		{"noext", "txt"},
	}
	for _, tt := range tests {
		if got := GetFileType(tt.filename); got != tt.want {
			t.Errorf("GetFileType(%q) = %q, want %q", tt.filename, got, tt.want)
		}
	}
}
