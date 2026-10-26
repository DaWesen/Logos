package strutil

import (
	"strings"
	"unicode/utf8"
)

// ExtractJSON 从 LLM 输出中提取 JSON 对象文本。
// 仅用于期望对象输出的场景；需要兼容数组时用 ExtractJSONValue。
func ExtractJSON(s string) string {
	return ExtractJSONValue(s)
}

// ExtractJSONValue 从 LLM 输出中提取第一个完整的 JSON 对象或数组文本。
// 处理流程：剥离 markdown 代码块围栏 → 从首个 '{' 或 '[' 开始按括号配对扫描，
// 遇到字符串内的括号与转义字符时跳过。扫描不到配对闭合时退化为取最后一个同类
// 闭合括号；仍找不到则原样返回，由调用方自行判错。
func ExtractJSONValue(s string) string {
	s = stripCodeFence(strings.TrimSpace(s))
	if s == "" {
		return s
	}

	start := strings.IndexAny(s, "{[")
	if start < 0 {
		return s
	}
	if end, ok := scanJSONEnd(s, start); ok {
		return s[start:end]
	}

	// 兜底：LLM 输出被截断或括号不配对，退化为最后一个闭合括号
	last := strings.LastIndexAny(s, "}]")
	if last > start {
		return s[start : last+1]
	}
	return s[start:]
}

// stripCodeFence 去掉 ```json ... ``` 或 ``` ... ``` 包装。
func stripCodeFence(s string) string {
	if strings.HasPrefix(s, "```") {
		if idx := strings.IndexByte(s, '\n'); idx >= 0 {
			s = s[idx+1:]
		} else {
			s = strings.TrimPrefix(s, "```")
		}
	}
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, "```") {
		s = strings.TrimSuffix(s, "```")
	}
	return strings.TrimSpace(s)
}

// scanJSONEnd 从 start 处的 '{' 或 '[' 开始配对扫描，返回闭合括号之后的位置。
// 括号不配对时返回 false。
func scanJSONEnd(s string, start int) (int, bool) {
	stack := make([]byte, 0, 8)
	inString := false
	escaped := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			stack = append(stack, '}')
		case '[':
			stack = append(stack, ']')
		case '}', ']':
			if len(stack) == 0 || stack[len(stack)-1] != c {
				return 0, false
			}
			stack = stack[:len(stack)-1]
			if len(stack) == 0 {
				return i + 1, true
			}
		}
	}
	return 0, false
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
