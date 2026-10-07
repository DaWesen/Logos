package strutil

import (
	"strings"
	"unicode/utf8"
)

// ExtractJSON 从 LLM 输出中提取 JSON 对象文本：
// 剥离 markdown 代码块包装，取首个 '{' 到末个 '}' 之间的内容。
// 找不到 JSON 结构时原样返回（调用方自行报错）。
func ExtractJSON(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	s = strings.TrimSpace(s)
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start >= 0 && end > start {
		return s[start : end+1]
	}
	return s
}

func CleanInvalidUTF8(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			i++
			continue
		}
		if r == 0 {
			i += size
			continue
		}
		b.WriteRune(r)
		i += size
	}
	return b.String()
}

func TruncateByRunes(s string, maxRunes int) string {
	runeCount := utf8.RuneCountInString(s)
	if maxRunes <= 0 || runeCount <= maxRunes {
		return s
	}
	runes := []rune(s)
	marker := "...(内容已截断)"
	reserve := utf8.RuneCountInString(marker)
	usable := maxRunes - reserve
	if usable <= 0 {
		usable = maxRunes
		marker = "..."
		if usable <= 0 {
			return ""
		}
	}
	headSize := usable * 7 / 10
	if headSize < 1 {
		headSize = 1
	}
	tailSize := usable - headSize
	if tailSize < 1 {
		return string(runes[:headSize]) + marker
	}
	return string(runes[:headSize]) + marker + string(runes[len(runes)-tailSize:])
}
