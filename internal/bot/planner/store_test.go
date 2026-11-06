package planner

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
)

// legacyStepInputHash 复刻加入 assumptions/expects 之前的指纹算法。
// 老运行记录用旧算法落库，续跑时必须仍能命中，否则一次升级
// 就会把所有可安全重放的副作用步骤变成"未执行过"。
func legacyStepInputHash(step PlanStep, prior string) string {
	h := sha256.New()
	fmt.Fprintf(h, "%d\n%s\n%s\n%s\n", step.ID, step.Tool, step.Description, prior)
	return hex.EncodeToString(h.Sum(nil))
}

func TestStepInputHash_AssumptionsAffectFingerprint(t *testing.T) {
	base := PlanStep{ID: 1, Tool: "file_read", Description: "读取配置文件"}
	changed := base
	changed.Assumptions = []string{"文件编码为 UTF-8"}

	if StepInputHash(base, "prior") == StepInputHash(changed, "prior") {
		t.Fatal("前提变化后输入指纹必须变化，否则副作用步骤会按旧前提重放")
	}

	other := base
	other.Assumptions = []string{"文件编码为 GBK"}
	if StepInputHash(changed, "prior") == StepInputHash(other, "prior") {
		t.Error("不同前提应产生不同指纹")
	}
}

func TestStepInputHash_ExpectsAffectFingerprint(t *testing.T) {
	base := PlanStep{ID: 1, Description: "生成摘要"}
	changed := base
	changed.Expects = "摘要不超过 200 字"

	if StepInputHash(base, "") == StepInputHash(changed, "") {
		t.Fatal("预期变化后输入指纹必须变化")
	}
}

func TestStepInputHash_StableWithoutOptionalFields(t *testing.T) {
	step := PlanStep{ID: 7, Tool: "doc_write", Description: "写入文档"}
	if got, want := StepInputHash(step, "前序结果"), legacyStepInputHash(step, "前序结果"); got != want {
		t.Fatalf("未声明前提/预期时应保持旧指纹以兼容已落库记录：got=%s want=%s", got, want)
	}
}
