package scache

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode"
)

// Normalize 做「不改变语义」的归一化：
//   - 全角转半角（含全角空格）
//   - 英文转小写
//   - 连续空白折叠为一个空格
//   - 去掉句末标点与首尾空白
//
// 刻意不删除内部标点与小数点：把 "1.5" 归一成 "15" 会让数字槽位
// 从「不同」变成「相同」，这是最危险的一类假命中。
func Normalize(query string) string {
	var b strings.Builder
	b.Grow(len(query))

	lastSpace := false
	for _, r := range query {
		switch {
		case r == '\u3000':
			r = ' '
		case r >= '\uFF01' && r <= '\uFF5E':
			r = r - 0xFEE0
		}

		if unicode.IsSpace(r) {
			if !lastSpace && b.Len() > 0 {
				b.WriteByte(' ')
				lastSpace = true
			}
			continue
		}
		lastSpace = false
		b.WriteRune(unicode.ToLower(r))
	}

	out := strings.TrimSpace(b.String())
	out = strings.TrimRightFunc(out, func(r rune) bool {
		switch r {
		case '。', '！', '？', '.', '!', '?', '，', ',', '；', ';':
			return true
		}
		return false
	})
	return strings.TrimSpace(out)
}

// NormalizeHash 是精确命中索引的字段名。
func NormalizeHash(normalized string) string {
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])[:16]
}
