package parser

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"

	"archive/zip"
)

type DocParser struct{}

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

	ext := strings.ToLower(filepath.Ext(filename))
	var content string

	switch ext {
	case ".pdf":
		content, err = parsePDF(docData)
	case ".docx":
		content, err = parseDOCX(docData)
	case ".pptx":
		content, err = parsePPTX(docData)
	case ".xlsx":
		content, err = parseXLSX(docData)
	case ".doc":
		content = extractTextFromBinary(docData)
	case ".xls":
		content = extractTextFromBinary(docData)
	case ".ppt":
		content = extractTextFromBinary(docData)
	case ".rtf":
		content = extractRTFText(string(docData))
	case ".odt":
		content, err = parseODT(docData)
	case ".ods":
		content, err = parseODS(docData)
	case ".odp":
		content, err = parseODP(docData)
	default:
		content = extractTextFromBinary(docData)
	}

	if err != nil {
		metadata["parse_error"] = err.Error()
		content = extractTextFromBinary(docData)
	}

	metadata["char_count"] = len(content)
	metadata["format"] = ext

	return content, metadata, nil
}

func (p *DocParser) SupportedTypes() []string {
	return []string{"pdf", "doc", "docx", "xls", "xlsx", "ppt", "pptx", "rtf", "odt", "ods", "odp"}
}

func parsePDF(data []byte) (string, error) {
	var textParts []string

	reText := regexp.MustCompile(`BT\s*([\s\S]*?)\s*ET`)
	matches := reText.FindAllSubmatch(data, -1)
	for _, match := range matches {
		if len(match) > 1 {
			block := string(match[1])
			tjRe := regexp.MustCompile(`\(([^)]*)\)\s*Tj`)
			tjMatches := tjRe.FindAllStringSubmatch(block, -1)
			for _, tj := range tjMatches {
				if len(tj) > 1 {
					textParts = append(textParts, tj[1])
				}
			}

			tjArrayRe := regexp.MustCompile(`\[(.*?)\]\s*TJ`)
			tjArrayMatches := tjArrayRe.FindAllStringSubmatch(block, -1)
			for _, tja := range tjArrayMatches {
				if len(tja) > 1 {
					parenRe := regexp.MustCompile(`\(([^)]*)\)`)
					parenMatches := parenRe.FindAllStringSubmatch(tja[1], -1)
					for _, pm := range parenMatches {
						if len(pm) > 1 {
							textParts = append(textParts, pm[1])
						}
					}
				}
			}
		}
	}

	if len(textParts) == 0 {
		return extractTextFromBinary(data), nil
	}

	result := strings.Join(textParts, " ")
	result = strings.TrimSpace(result)
	if result == "" {
		return extractTextFromBinary(data), nil
	}
	return result, nil
}

func parseDOCX(data []byte) (string, error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("打开docx失败: %w", err)
	}

	var textParts []string
	for _, f := range r.File {
		if f.Name == "word/document.xml" {
			rc, err := f.Open()
			if err != nil {
				continue
			}
			defer rc.Close()
			content, err := io.ReadAll(rc)
			if err != nil {
				continue
			}
			text := extractXMLText(content)
			textParts = append(textParts, text)
		}
	}

	return strings.Join(textParts, "\n"), nil
}

func parsePPTX(data []byte) (string, error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("打开pptx失败: %w", err)
	}

	var slides []string
	for _, f := range r.File {
		if strings.HasPrefix(f.Name, "ppt/slides/slide") && strings.HasSuffix(f.Name, ".xml") {
			rc, err := f.Open()
			if err != nil {
				continue
			}
			defer rc.Close()
			content, err := io.ReadAll(rc)
			if err != nil {
				continue
			}
			text := extractXMLText(content)
			slides = append(slides, text)
		}
	}

	return strings.Join(slides, "\n\n--- Slide ---\n\n"), nil
}

func parseXLSX(data []byte) (string, error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("打开xlsx失败: %w", err)
	}

	var sheets []string
	for _, f := range r.File {
		if strings.HasPrefix(f.Name, "xl/worksheets/sheet") && strings.HasSuffix(f.Name, ".xml") {
			rc, err := f.Open()
			if err != nil {
				continue
			}
			defer rc.Close()
			content, err := io.ReadAll(rc)
			if err != nil {
				continue
			}
			text := extractXMLText(content)
			sheets = append(sheets, text)
		}
	}

	sharedStrings := make([]string, 0)
	for _, f := range r.File {
		if f.Name == "xl/sharedStrings.xml" {
			rc, err := f.Open()
			if err != nil {
				continue
			}
			defer rc.Close()
			content, err := io.ReadAll(rc)
			if err != nil {
				continue
			}
			text := extractXMLText(content)
			sharedStrings = append(sharedStrings, text)
		}
	}

	result := strings.Join(sheets, "\n\n--- Sheet ---\n\n")
	if len(sharedStrings) > 0 {
		result = strings.Join(sharedStrings, "\n") + "\n" + result
	}
	return result, nil
}

func parseODT(data []byte) (string, error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("打开odt失败: %w", err)
	}

	for _, f := range r.File {
		if f.Name == "content.xml" {
			rc, err := f.Open()
			if err != nil {
				continue
			}
			defer rc.Close()
			content, err := io.ReadAll(rc)
			if err != nil {
				continue
			}
			return extractXMLText(content), nil
		}
	}
	return "", nil
}

func parseODS(data []byte) (string, error) {
	return parseODT(data)
}

func parseODP(data []byte) (string, error) {
	return parseODT(data)
}

func extractXMLText(data []byte) string {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	var textParts []string
	var inText bool

	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}

		switch t := token.(type) {
		case xml.StartElement:
			local := t.Name.Local
			if local == "t" || local == "r" || local == "p" || local == "a" ||
				local == "span" || local == "text" || local == "h" ||
				local == "table-cell" || local == "si" {
				inText = true
			}
		case xml.CharData:
			if inText {
				s := strings.TrimSpace(string(t))
				if s != "" {
					textParts = append(textParts, s)
				}
			}
		case xml.EndElement:
			local := t.Name.Local
			if local == "t" || local == "r" || local == "p" || local == "a" ||
				local == "span" || local == "text" || local == "h" ||
				local == "table-cell" || local == "si" {
				inText = false
			}
			if local == "p" || local == "si" || local == "h" {
				textParts = append(textParts, "\n")
			}
		}
	}

	result := strings.Join(textParts, " ")
	result = regexp.MustCompile(`\n\s+`).ReplaceAllString(result, "\n")
	result = regexp.MustCompile(`\s{2,}`).ReplaceAllString(result, " ")
	return strings.TrimSpace(result)
}

func extractTextFromBinary(data []byte) string {
	var textParts []string
	re := regexp.MustCompile(`[\p{Han}\p{Hiragana}\p{Katakana}a-zA-Z][\p{Han}\p{Hiragana}\p{Katakana}a-zA-Z0-9\s.,;:!?'"()\-_@#$%&+=/\\{}\[\]<>]{2,}`)
	matches := re.FindAll(data, -1)
	for _, m := range matches {
		s := strings.TrimSpace(string(m))
		if len(s) > 3 {
			textParts = append(textParts, s)
		}
	}
	if len(textParts) == 0 {
		return fmt.Sprintf("[Binary Document: %d bytes]", len(data))
	}
	return strings.Join(textParts, "\n")
}

func extractRTFText(rtf string) string {
	re := regexp.MustCompile(`\\[a-z]+\d*\s?`)
	text := re.ReplaceAllString(rtf, "")
	text = regexp.MustCompile(`[{}]`).ReplaceAllString(text, "")
	text = strings.ReplaceAll(text, "\\par", "\n")
	text = strings.ReplaceAll(text, "\\line", "\n")
	text = regexp.MustCompile(`\\\n`).ReplaceAllString(text, "\n")
	text = regexp.MustCompile(`\s{2,}`).ReplaceAllString(text, " ")
	return strings.TrimSpace(text)
}
