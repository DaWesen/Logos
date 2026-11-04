// Package structured 统一处理 LLM 的结构化输出：提取 JSON、解析、失败后定向修复。
//
// 为什么要单独一层：各业务模块原来各自 json.Unmarshal，解析失败就降级为空结果，
// 失败率只能靠日志猜。收口后同时解决三件事——统一的 JSON 提取、最多一次修复重试、
// 解析/修复结果可度量。
package structured

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"Logos/pkg/obs"
	"Logos/pkg/strutil"
)

// repairRawLimit 修复 prompt 里原始输出的上限。
// 解析失败时原始输出可能很长，整段回灌会放大一次修复调用的成本；
// 保留头尾是因为常见问题（缺右括号、尾逗号）往往出现在末尾。
const repairRawLimit = 4000

// Parse 从模型输出中提取 JSON 并反序列化。
func Parse(raw string, out interface{}) error {
	return json.Unmarshal([]byte(strutil.ExtractJSONValue(raw)), out)
}

// RepairFunc 在首次解析失败后被调用，返回模型修正后的原始输出。
// 实现方负责真正发起模型调用；返回 error 表示修复调用本身失败。
type RepairFunc func(ctx context.Context, raw string, parseErr error) (string, error)

// ParseWithRepair 解析 JSON；首次失败时最多调用一次 repair，再解析一次。
//
// 返回最终使用的原始文本：修复成功时是修复后的文本，失败时是第一次的文本。
// 指标语义：
//   - logos.llm.parse.count 只记录首次解析结果，失败率仍然真实可见；
//   - logos.llm.repair.count 记录修复是否奏效，用来判断「重试有没有价值」。
//
// 不在这里无限重试：修复是一次定向纠错，不是重试风暴；
// 两次都失败就交给业务降级，避免把一次坏输出放大成多次模型调用。
func ParseWithRepair(
	ctx context.Context, module, raw string, out interface{}, repair RepairFunc,
) (string, error) {
	firstErr := Parse(raw, out)
	if firstErr == nil {
		obs.RecordLLMParse(ctx, module, "ok")
		return raw, nil
	}
	obs.RecordLLMParse(ctx, module, "failed")

	if repair == nil {
		return raw, firstErr
	}

	fixed, repairErr := repair(ctx, raw, firstErr)
	if repairErr != nil {
		obs.RecordLLMRepair(ctx, module, "failed")
		return raw, firstErr
	}
	if err := Parse(fixed, out); err != nil {
		obs.RecordLLMRepair(ctx, module, "failed")
		return fixed, firstErr
	}

	obs.RecordLLMRepair(ctx, module, "fixed")
	return fixed, nil
}

// RepairPrompt 构造定向修复提示。
// schemaHint 由调用方给出期望结构（例如 `{"memories":[...]}`），
// 比笼统地说「输出合法 JSON」更容易让模型改对。
func RepairPrompt(raw string, parseErr error, schemaHint string) string {
	var b strings.Builder
	b.WriteString("上一次输出无法解析为 JSON。\n解析错误：")
	b.WriteString(parseErr.Error())
	b.WriteString("\n\n请修正后只输出合法 JSON，不要输出解释、Markdown 代码块或任何额外文字。\n")
	if schemaHint != "" {
		b.WriteString("期望结构：")
		b.WriteString(schemaHint)
		b.WriteString("\n")
	}
	b.WriteString("\n原始输出：\n")
	b.WriteString(truncateMiddle(raw, repairRawLimit))
	return b.String()
}

// truncateMiddle 保留头尾、省略中间，避免把超长输出整段塞回模型。
func truncateMiddle(s string, limit int) string {
	runes := []rune(s)
	if limit <= 0 || len(runes) <= limit {
		return s
	}
	head := limit * 2 / 3
	tail := limit - head
	return fmt.Sprintf("%s\n...[中间省略 %d 字符]...\n%s",
		string(runes[:head]), len(runes)-limit, string(runes[len(runes)-tail:]))
}
