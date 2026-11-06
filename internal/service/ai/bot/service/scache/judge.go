package scache

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Verdict 是灰区判官的结论。
type Verdict string

const (
	JudgeSame       Verdict = "same"
	JudgeDifferent  Verdict = "different"
	JudgeUnparsable Verdict = "unparsable"
	JudgeTimeout    Verdict = "timeout"
	JudgeError      Verdict = "error"
	JudgeDisabled   Verdict = "disabled"
)

// judgeSystemPrompt 是判官的固定系统提示。
//
// 关键是最后一句「任何一条不确定 → DIFFERENT」：缓存的收益是省一次
// 生成，代价是用户拿到错误答案。两者不对称，所以默认保守。
const judgeSystemPrompt = `你是缓存命中判定器。给你一个"新问题"和若干"历史问题+答案"。
判断：历史答案能否原样回答新问题。

判定为 SAME 的条件（全部满足）：
1. 两个问题的诉求相同（不是"相关"，是"同一个诉求"）
2. 关键实体、数量、时间范围、否定与否完全一致
3. 历史答案不依赖当时的上下文（无"上面提到的""刚才说的"）
4. 答案中的时效信息仍然有效

任何一条不确定 → 判 DIFFERENT。宁可漏命中，不可错命中。

只输出一行：SAME 或 DIFFERENT，然后一个空格，接不超过 20 字的理由。`

// JudgeSystemPrompt 供装配层把判官接到具体的模型客户端。
// 提示词留在缓存包里，避免装配层改一个字就悄悄改变了判定口径。
func JudgeSystemPrompt() string { return judgeSystemPrompt }

// BuildJudgePrompt 拼接判官输入。候选答案截断到 500 字：
// 判官只需要判断「能不能答」，不需要读完整个长答案。
func BuildJudgePrompt(query string, cands []Candidate) string {
	var b strings.Builder
	b.WriteString("新问题：")
	b.WriteString(query)
	b.WriteString("\n历史候选：\n")
	for i, c := range cands {
		if i >= 3 {
			break
		}
		answer := truncateRunes(c.Entry.Answer, 500)
		fmt.Fprintf(&b, "[%d] 问题：%s\n    答案：%s\n", i+1, c.Entry.Query, answer)
	}
	return b.String()
}

// ParseVerdict 解析模型输出。无法解析一律按 DIFFERENT 处理。
func ParseVerdict(raw string) Verdict {
	trimmed := strings.TrimSpace(raw)
	upper := strings.ToUpper(trimmed)
	switch {
	case strings.HasPrefix(upper, "SAME"):
		return JudgeSame
	case strings.HasPrefix(upper, "DIFFERENT"):
		return JudgeDifferent
	default:
		return JudgeUnparsable
	}
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	runes := []rune(s)
	return string(runes[:n]) + "…"
}

// isSentenceEnd 用于伪流式回放的分块边界。
func isSentenceEnd(r rune) bool {
	switch r {
	case '。', '！', '？', '；', '.', '!', '?', ';', '\n':
		return true
	}
	return false
}
