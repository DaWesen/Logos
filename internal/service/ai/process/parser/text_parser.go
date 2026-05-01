package parser

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

type TextParser struct {
}

func NewTextParser() *TextParser {
	return &TextParser{}
}

func (p *TextParser) Parse(ctx context.Context, reader io.Reader, filename string) (string, map[string]interface{}, error) {
	content, err := io.ReadAll(reader)
	if err != nil {
		return "", nil, fmt.Errorf("读取文件失败: %w", err)
	}

	text := string(content)
	metadata := make(map[string]interface{})
	metadata["file_name"] = filename
	metadata["size"] = len(content)
	metadata["lines"] = countLines(text)
	metadata["words"] = countWords(text)

	return text, metadata, nil
}

func (p *TextParser) SupportedTypes() []string {
	return []string{"txt", "md", "csv", "json", "yaml", "yml", "html", "xml"}
}

func countLines(text string) int {
	scanner := bufio.NewScanner(strings.NewReader(text))
	count := 0
	for scanner.Scan() {
		count++
	}
	return count
}

func countWords(text string) int {
	words := strings.Fields(text)
	return len(words)
}

func GetFileType(filename string) string {
	ext := filepath.Ext(filename)
	if ext == "" {
		return "txt"
	}
	return strings.ToLower(ext[1:])
}
