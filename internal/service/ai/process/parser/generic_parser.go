package parser

import (
	"context"
	"fmt"
	"io"
)

type GenericParser struct {
}

func NewGenericParser() *GenericParser {
	return &GenericParser{}
}

func (p *GenericParser) Parse(ctx context.Context, reader io.Reader, filename string) (string, map[string]interface{}, error) {
	data, err := io.ReadAll(reader)
	if err != nil {
		return "", nil, fmt.Errorf("读取文件失败: %w", err)
	}

	metadata := make(map[string]interface{})
	metadata["file_name"] = filename
	metadata["size"] = len(data)

	content := fmt.Sprintf("[文件: %s, 大小: %d 字节]", filename, len(data))

	return content, metadata, nil
}

func (p *GenericParser) SupportedTypes() []string {
	return []string{"*"}
}
