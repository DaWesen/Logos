package agent

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/cloudwego/eino/schema"
)

// historyRuneBudget 历史消息进入 prompt 的总字符预算（不含 system prompt 与当前消息）。
//
// 对话越长，历史越容易把上下文窗口吃掉：既推高成本，也挤压工具结果与记忆的可用空间，
// 最终表现为「越聊越笨」——最近的指令被大量陈年历史淹没。这里按字符估算 token：
// 中英混合场景下 1 字符 ≈ 0.5~1 token，12000 字符约 6k~12k token，留足回答与工具预算。
const historyRuneBudget = 12000

// messageRunes 估算单条消息占用的字符数。
// 除了正文，工具调用的函数名与参数同样进入 prompt，漏算会让预算系统性偏低。
func messageRunes(m *schema.Message) int {
	if m == nil {
		return 0
	}
	n := utf8.RuneCountInString(m.Content)
	for _, tc := range m.ToolCalls {
		n += utf8.RuneCountInString(tc.Function.Name)
		n += utf8.RuneCountInString(tc.Function.Arguments)
	}
	return n
}

// trimHistory 从最近的消息向前保留，直到预算用尽。
//
// 返回被保留的切片与被省略的条数。两条不变式：
//  1. 不产生「孤立的工具结果」——kept 的第一条不能是 Role=Tool，
//     否则它对应的 assistant tool_calls 已被截掉，供应商会直接 400；
//  2. 不切断「最后一条 assistant 的 tool_calls 与随后 tool 结果」的配对，
//     从后往前累积天然满足，唯一需要修正的是边界处的前向孤儿。
func trimHistory(history []*schema.Message, budget int) ([]*schema.Message, int) {
	if len(history) == 0 {
		return nil, 0
	}
	if budget <= 0 {
		budget = historyRuneBudget
	}

	used := 0
	start := len(history)
	for i := len(history) - 1; i >= 0; i-- {
		cost := messageRunes(history[i])
		if used+cost > budget && start < len(history) {
			break
		}
		used += cost
		start = i
	}

	// 修正边界孤儿：保留下来的第一条若是 tool 结果，其 assistant 调用已被截掉，
	// 只能把它一起丢掉，直到第一条不再是 Tool。
	for start < len(history) && history[start] != nil && history[start].Role == schema.Tool {
		start++
	}

	if start == 0 {
		return history, 0
	}
	kept := history[start:]
	omitted := start

	// 全是 nil / 空消息时 kept 可能是空的，此时按「全部省略」处理更省预算。
	if len(kept) == 0 {
		return nil, len(history)
	}
	return kept, omitted
}

// historyNotice 生成「历史被截断」的提示，拼进 system prompt。
//
// 不单独插入一条 system/user 消息：部分供应商对多条 system 或
// tool 之后的 user 消息有严格校验，拼进现有 system prompt 最安全。
func historyNotice(omitted int) string {
	var sb strings.Builder
	sb.WriteString("\n\n注意：为控制上下文长度，较早的 ")
	sb.WriteString(fmt.Sprintf("%d", omitted))
	sb.WriteString(" 条对话已省略；如用户追问早期内容，请先说明你不记得具体细节。")
	return sb.String()
}
