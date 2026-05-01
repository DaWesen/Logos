package parser

import (
	"context"
	"fmt"
	"io"
)

type DocParser struct {
}

func NewDocParser() *DocParser {
	return &DocParser{}
}

func (p *DocParser) Parse(ctx context.Context, reader io.Reader, filename string) (string, map[string]interface{}, error) {
	docData, err := io.ReadAll(reader)
	if err != nil {
		return "", nil, fmt.Errorf("读取文档文件失败: %w", err)
	}

	metadata := make(map[string]interface{})
	metadata["file_name"] = filename
	metadata["size"] = len(docData)
	metadata["type"] = "document"

	content := fmt.Sprintf("[Document File: %s, Size: %d bytes]", filename, len(docData))

	return content, metadata, nil
}

func (p *DocParser) SupportedTypes() []string {
	return []string{"pdf", "doc", "docx", "xls", "xlsx", "ppt", "pptx", "rtf", "odt", "ods", "odp"}
}
