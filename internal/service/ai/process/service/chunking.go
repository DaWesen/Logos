package service

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	defaultChunkSize    = 1000
	defaultChunkOverlap = 100
)

var headingPrefixRe = regexp.MustCompile(`^#{1,6}(\s|$)`)

// isHeading 判断是否为 Markdown ATX 标题行（# ~ ######）
func isHeading(line string) bool {
	return headingPrefixRe.MatchString(strings.TrimSpace(line))
}

// SplitTextIntoChunks 将文本分块，策略为「结构感知」：
//  1. 优先按 Markdown 标题边界切分，每个小节尽量独立成块，标题保留在块内（检索上下文更完整）；
//  2. 相邻短小节合并，避免碎片化；
//  3. 超长小节降级为固定窗口 + 尾部重叠切分（按行边界），与无结构文本行为一致。
func SplitTextIntoChunks(content string, maxChunkSize, overlap int) []string {
	if strings.TrimSpace(content) == "" {
		return nil
	}
	if maxChunkSize <= 0 {
		maxChunkSize = defaultChunkSize
	}
	if overlap < 0 || overlap >= maxChunkSize {
		overlap = defaultChunkOverlap
	}

	var chunks []string
	var buf string

	flush := func() {
		if strings.TrimSpace(buf) != "" {
			chunks = append(chunks, strings.TrimRight(buf, "\n"))
		}
		buf = ""
	}

	for _, section := range splitByHeadings(content) {
		section = strings.TrimRight(section, "\n")
		if strings.TrimSpace(section) == "" {
			continue
		}

		// 超长小节：冲刷缓冲后按固定窗口切分
		if len(section) > maxChunkSize {
			flush()
			chunks = append(chunks, splitByWindow(section, maxChunkSize, overlap)...)
			continue
		}

		// 相邻短小节合并
		if buf != "" && len(buf)+1+len(section) > maxChunkSize {
			flush()
		}
		if buf == "" {
			buf = section
		} else {
			buf += "\n" + section
		}
	}
	flush()

	return chunks
}

// splitByHeadings 按标题行把文档切成小节，每节以标题行开头（首个小节可能没有标题）
func splitByHeadings(content string) []string {
	lines := strings.Split(content, "\n")
	var sections []string
	var cur strings.Builder

	for _, line := range lines {
		if isHeading(line) && strings.TrimSpace(cur.String()) != "" {
			sections = append(sections, cur.String())
			cur.Reset()
		}
		cur.WriteString(line)
		cur.WriteString("\n")
	}
	if strings.TrimSpace(cur.String()) != "" {
		sections = append(sections, cur.String())
	}
	return sections
}

// splitByWindow 按行边界做固定窗口切分，块间保留尾部重叠内容。
// 超过窗口大小的单行会被按 rune 边界硬切，避免绕过大小限制。
func splitByWindow(section string, maxChunkSize, overlap int) []string {
	lines := strings.Split(section, "\n")
	var chunks []string
	var current strings.Builder

	flushCurrent := func() {
		if strings.TrimSpace(current.String()) != "" {
			chunks = append(chunks, strings.TrimRight(current.String(), "\n"))
		}
		current.Reset()
	}

	for _, line := range lines {
		// 超长行：冲刷缓冲后按 rune 边界硬切成独立块
		if len(line) > maxChunkSize {
			flushCurrent()
			chunks = append(chunks, splitLongLine(line, maxChunkSize)...)
			continue
		}

		if current.Len()+len(line) > maxChunkSize && current.Len() > 0 {
			chunks = append(chunks, strings.TrimRight(current.String(), "\n"))

			tail := current.String()
			if len(tail) > overlap {
				tail = tail[len(tail)-overlap:]
			}
			current.Reset()
			current.WriteString(tail)
		}
		current.WriteString(line)
		current.WriteString("\n")
	}
	if strings.TrimSpace(current.String()) != "" {
		chunks = append(chunks, strings.TrimRight(current.String(), "\n"))
	}
	return chunks
}

// splitLongLine 将超长行按字节上限硬切，切点落在 rune 边界上避免破坏多字节字符
func splitLongLine(line string, maxLen int) []string {
	var parts []string
	var cur []rune
	curBytes := 0

	for _, r := range line {
		size := utf8.RuneLen(r)
		if curBytes+size > maxLen && len(cur) > 0 {
			parts = append(parts, string(cur))
			cur = nil
			curBytes = 0
		}
		cur = append(cur, r)
		curBytes += size
	}
	if len(cur) > 0 {
		parts = append(parts, string(cur))
	}
	return parts
}
