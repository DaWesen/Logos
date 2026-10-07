package service

import (
	"context"
	"testing"

	botmodel "Logos/internal/service/ai/bot/model"
)

// ==================== reflectionEnabled ====================

func TestReflectionEnabled_NilBot(t *testing.T) {
	if reflectionEnabled(nil) {
		t.Error("nil bot 不应启用反思")
	}
}

func TestReflectionEnabled_NoConfig(t *testing.T) {
	bot := &botmodel.Bot{Config: botmodel.JSONMap{}}
	if reflectionEnabled(bot) {
		t.Error("无配置不应启用反思")
	}
}

func TestReflectionEnabled_True(t *testing.T) {
	bot := &botmodel.Bot{Config: botmodel.JSONMap{"enable_reflection": "true"}}
	if !reflectionEnabled(bot) {
		t.Error("配置 enable_reflection=true 应启用反思")
	}
}

func TestReflectionEnabled_False(t *testing.T) {
	bot := &botmodel.Bot{Config: botmodel.JSONMap{"enable_reflection": "false"}}
	if reflectionEnabled(bot) {
		t.Error("配置 enable_reflection=false 不应启用反思")
	}
}

// ==================== parseReflection（纯函数） ====================
//
// reflectAnswer 的决策逻辑（pass→原样 / fail+revised→替换 / fail无revised→原样 /
// 解析失败→原样）都建立在 parseReflection 的输出上，因此纯函数测试覆盖了
// 反思的核心契约。reflectAnswer 自身只额外验证：LLM 调用失败时原样返回
// （质检是增益不是闸门）——见下方的容错测试。

func TestParseReflection_Pass(t *testing.T) {
	raw := `{"pass": true, "issues": "", "revised": ""}`
	r, err := parseReflection(raw)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if !r.Pass {
		t.Error("pass 应为 true")
	}
	if r.Revised != "" {
		t.Errorf("pass 时 revised 应为空，实际 %q", r.Revised)
	}
}

func TestParseReflection_FailWithRevised(t *testing.T) {
	raw := `{"pass": false, "issues": "答案未提及关键要求", "revised": "修订后的答案"}`
	r, err := parseReflection(raw)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if r.Pass {
		t.Error("pass 应为 false")
	}
	if r.Issues != "答案未提及关键要求" {
		t.Errorf("issues 字段错: %q", r.Issues)
	}
	if r.Revised != "修订后的答案" {
		t.Errorf("revised 字段错: %q", r.Revised)
	}
}

func TestParseReflection_MarkdownWrapped(t *testing.T) {
	// LLM 输出常带 markdown 代码块包装，应能剥离
	raw := "```json\n{\"pass\": true, \"issues\": \"\", \"revised\": \"\"}\n```"
	r, err := parseReflection(raw)
	if err != nil {
		t.Fatalf("剥离 markdown 后应解析成功: %v", err)
	}
	if !r.Pass {
		t.Error("pass 应为 true")
	}
}

func TestParseReflection_ExtraTextAround(t *testing.T) {
	// LLM 可能在 JSON 前后加解释文字
	raw := `审查结果如下：{"pass": true, "issues": "", "revised": ""} 审查完成。`
	r, err := parseReflection(raw)
	if err != nil {
		t.Fatalf("应能从前后带文字的输出中提取 JSON: %v", err)
	}
	if !r.Pass {
		t.Error("pass 应为 true")
	}
}

func TestParseReflection_InvalidJSON(t *testing.T) {
	raw := `这不是 JSON`
	if _, err := parseReflection(raw); err == nil {
		t.Error("非法 JSON 应返回错误")
	}
}

func TestParseReflection_PartialFields(t *testing.T) {
	// 只给 pass 字段，issues/revised 缺失应 zero-value
	raw := `{"pass": true}`
	r, err := parseReflection(raw)
	if err != nil {
		t.Fatalf("部分字段缺失应能解析: %v", err)
	}
	if !r.Pass {
		t.Error("pass 应为 true")
	}
	if r.Issues != "" || r.Revised != "" {
		t.Errorf("缺失字段应为空字符串")
	}
}

func TestParseReflection_BareCodeFence(t *testing.T) {
	// 不带 json 语言标签的代码块
	raw := "```\n{\"pass\": false, \"issues\": \"x\", \"revised\": \"y\"}\n```"
	r, err := parseReflection(raw)
	if err != nil {
		t.Fatalf("裸代码块应能解析: %v", err)
	}
	if r.Pass || r.Revised != "y" {
		t.Errorf("解析内容错: %+v", r)
	}
}

// ==================== reflectAnswer（容错路径） ====================

func TestReflectAnswer_NoPlannerModel_Passthrough(t *testing.T) {
	// einoManager 为 nil → plannerChat 报错 → 反思失败 → 原答案直出
	// （质检是增益不是闸门：反思本身失败不应阻塞主流程）
	s := &botServiceImpl{}
	orig := "原始答案"
	got, changed := s.reflectAnswer(context.Background(), "问题", orig)
	if changed {
		t.Error("反思失败时不应标记为已修订")
	}
	if got != orig {
		t.Errorf("反思失败应原样返回，期望 %q，实际 %q", orig, got)
	}
}
