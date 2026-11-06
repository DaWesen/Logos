package scache

import (
	"time"
	"unicode/utf8"
)

// SplitForReplay 把整段答案切成接近逐 token 观感的小块。
//
// 优先在标点后断句：块太小会让前端频繁重排，块太大又像「一次性返回」。
// 超过 chunk 后再多看 8 个 rune，遇到句末标点就提前收口。
func SplitForReplay(answer string, chunkRunes int) []string {
	if answer == "" {
		return nil
	}
	if chunkRunes <= 0 {
		chunkRunes = 24
	}

	runes := []rune(answer)
	out := make([]string, 0, len(runes)/chunkRunes+1)
	start := 0
	count := 0
	for i, r := range runes {
		count++
		if count < chunkRunes {
			continue
		}
		if isSentenceEnd(r) || count >= chunkRunes+8 || i == len(runes)-1 {
			out = append(out, string(runes[start:i+1]))
			start = i + 1
			count = 0
		}
	}
	if start < len(runes) {
		out = append(out, string(runes[start:]))
	}
	return out
}

// ReplayAsStream 按块回放缓存答案。
//
// 总时长受 budget 约束，单块间隔受 maxDelay 约束：缓存命中的价值是快，
// 不能为了「像流式」把 400ms 的预算拖成按字数线性增长。
func ReplayAsStream(answer string, chunkRunes int, budget, maxDelay time.Duration, onChunk func(string) error) error {
	chunks := SplitForReplay(answer, chunkRunes)
	if len(chunks) == 0 {
		return nil
	}

	delay := time.Duration(0)
	if budget > 0 && len(chunks) > 1 {
		delay = budget / time.Duration(len(chunks)-1)
	}
	if maxDelay > 0 && delay > maxDelay {
		delay = maxDelay
	}

	for i, chunk := range chunks {
		if err := onChunk(chunk); err != nil {
			return err
		}
		if i < len(chunks)-1 && delay > 0 {
			time.Sleep(delay)
		}
	}
	return nil
}

// RuneLen 是包内常用的小工具，避免多处重复 []rune 转换。
func RuneLen(s string) int { return utf8.RuneCountInString(s) }
