package memory

import (
	"strings"

	botmodel "Logos/internal/service/ai/bot/model"
)

// ==================== 记忆置信度生命周期 ====================
//
// 参考 distill 置信度管线的设计哲学：
//
//  1. 三态增量合并——新抽取与既有记忆的关系决定置信度演化：
//       确认   值相同        → +0.05 封顶 0.98（重复确认≠绝对真理）
//       补充   新值包含旧值   → max(旧,新)，值更新（细节增加）
//       矛盾   值不同        → min(旧,新)−0.15 保底 0.2，
//                            保留双结论不自动选边（不同场景确实可能不同）
//  2. 越靠近 prompt 门槛越高：<0.5 的记忆不注入（agent 不会因为
//     标注就当参考），[0.5,0.65) 注入但标注「证据较少」；
//  3. 任何路径都到不了 1.0：确认封顶 0.98，统计不确定性永远存在；
//  4. 手动记忆 LLM 永不覆盖：用户改完一句话，下一轮抽取就改回去
//     的话，他不会再改第二次。

// Relation 新旧记忆的三态关系
type Relation string

const (
	RelationInsert   Relation = "insert"   // 首次出现
	RelationConfirm  Relation = "confirm"   // 值相同（独立确认）
	RelationSupplement Relation = "supplement" // 新值包含旧值（细节增加）
	RelationConflict Relation = "conflict"  // 值矛盾
	RelationUserOverride Relation = "user_override" // 手动记忆保护
)

const (
	confirmStep     = 0.05 // 每次独立确认的置信增量
	confirmCap      = 0.98 // 确认封顶：重复确认不等于绝对真理
	conflictStep    = 0.15 // 矛盾的置信惩罚
	conflictFloor    = 0.2  // 矛盾保底：结论仍可查证，不归零
	maxEvidence     = 50    // 单条记忆的证据上限
)

// ClassifyRelation 判定新值与既有记忆的关系（纯函数，可单测）。
// manual 来源的既有记忆由调用方在合并前跳过（user_override）。
func ClassifyRelation(existingValue, newValue string) Relation {
	if existingValue == newValue {
		return RelationConfirm
	}
	// 新值包含旧值视为"补充"：细节增加而非推翻
	if existingValue != "" && strings.Contains(newValue, existingValue) {
		return RelationSupplement
	}
	return RelationConflict
}

// ApplyMemoryMerge 把新抽取的记忆合并进既有记忆（纯函数，不写库）。
// 返回合并结果；RelationInsert 时返回 nil（调用方走新建路径）。
//
// 值语义：确认不重写值；补充/矛盾取新值（矛盾时旧值保留在 ConflictValue）。
// 证据语义：一律合并去重（新的在前，审阅时更可能想看最近的例子），截断上限。
func ApplyMemoryMerge(existing *botmodel.UserMemory, newValue string, newConfidence float64, newEvidence []string) (*botmodel.UserMemory, Relation) {
	// 手动记忆：LLM 永不覆盖
	if existing.Source == "manual" {
		return existing, RelationUserOverride
	}

	relation := ClassifyRelation(existing.Value, newValue)
	merged := *existing
	merged.Evidence = mergeEvidence(existing.Evidence, newEvidence)

	switch relation {
	case RelationConfirm:
		// 确认：值不动，置信小幅上升封顶 0.98
		merged.Confidence = minF(confirmCap, existing.Confidence+confirmStep)
		merged.ConflictValue = "" // 矛盾被新证据确认消除
		return &merged, RelationConfirm

	case RelationSupplement:
		// 补充：细节增加，值取新，置信取两者较大
		merged.Value = newValue
		merged.Confidence = maxF(existing.Confidence, newConfidence)
		return &merged, RelationSupplement

	default: // RelationConflict
		// 矛盾：降置信保底 0.2，值取新的但保留旧值为双结论
		merged.Value = newValue
		merged.Confidence = maxF(conflictFloor, minF(existing.Confidence, newConfidence)-conflictStep)
		merged.ConflictValue = existing.Value
		return &merged, RelationConflict
	}
}

// mergeEvidence 合并证据：新证据在前（更可能想看最近的例子），去重，截断上限
func mergeEvidence(existing, incoming []string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, id := range incoming {
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	for _, id := range existing {
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	if len(out) > maxEvidence {
		out = out[:maxEvidence]
	}
	return out
}

func minF(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func maxF(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// ==================== Prompt 注入的双档消费 ====================

const (
	// memoryMinConfidence 低于此值的记忆不注入 prompt：
	// agent 不会因为一条结论标注着低置信度就不当真——它只会看到
	// 一句陈述。审阅接口可以显示全部（给人看的），进 prompt 的必须站得住。
	memoryMinConfidence = 0.5
	// memoryThinConfidence 低于此值注入但标注「证据较少」：
	// 只排序不标注，0.55 与 0.9 的记忆在 agent 眼里长得一样。
	memoryThinConfidence = 0.65
	// memoryPerCategoryLimit 单类记忆注入上限，防止 prompt 膨胀
	memoryPerCategoryLimit = 10
)

// RenderMemoryLine 渲染单条记忆为 prompt 行（纯函数，可单测）：
// 双档标注 + 矛盾双结论声明。
func RenderMemoryLine(mem *botmodel.UserMemory) string {
	line := "- " + mem.Key + ": " + mem.Value

	var marks []string
	if mem.Confidence < memoryThinConfidence {
		marks = append(marks, "⚠︎证据较少")
	}
	if mem.ConflictValue != "" {
		marks = append(marks, "另有较早的不同说法: "+mem.ConflictValue)
	}
	if len(marks) > 0 {
		line += "（" + strings.Join(marks, "；") + "）"
	}
	return line
}

// ShouldInjectMemory 双档门槛：<0.5 不注入（纯函数，可单测）
func ShouldInjectMemory(mem *botmodel.UserMemory) bool {
	return mem.Confidence >= memoryMinConfidence
}
