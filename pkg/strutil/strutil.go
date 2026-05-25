package strutil

import (
	"strings"
	"unicode/utf8"
)

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
