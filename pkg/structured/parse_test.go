package structured

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestParse_HandlesCommonWrappers(t *testing.T) {
	var out struct {
		Name string `json:"name"`
	}
	raw := "好的，结果如下：\n```json\n{\"name\":\"logos\"}\n```\n以上。"
	if err := Parse(raw, &out); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if out.Name != "logos" {
		t.Fatalf("name = %q, want logos", out.Name)
	}
}

func TestParseWithRepair_NoRepairOnSuccess(t *testing.T) {
	var out []string
	called := false
	_, err := ParseWithRepair(context.Background(), "test", `["a","b"]`, &out,
		func(context.Context, string, error) (string, error) {
			called = true
			return "", errors.New("不应调用")
		})
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if called {
		t.Fatal("首次解析成功时不应触发修复")
	}
	if len(out) != 2 || out[0] != "a" {
		t.Fatalf("out = %v, want [a b]", out)
	}
}

func TestParseWithRepair_FixesOnce(t *testing.T) {
	var out struct {
		Memories []string `json:"memories"`
	}
	calls := 0
	gotErr := error(nil)
	raw, err := ParseWithRepair(context.Background(), "test", `{"memories":["a",]}`, &out,
		func(_ context.Context, _ string, parseErr error) (string, error) {
			calls++
			gotErr = parseErr
			return `{"memories":["a"]}`, nil
		})
	if err != nil {
		t.Fatalf("修复后应成功: %v", err)
	}
	if calls != 1 {
		t.Fatalf("修复次数 = %d, want 1", calls)
	}
	if gotErr == nil {
		t.Fatal("修复回调应收到首次解析错误")
	}
	if raw != `{"memories":["a"]}` {
		t.Fatalf("应返回修复后的文本，实际 %q", raw)
	}
	if len(out.Memories) != 1 || out.Memories[0] != "a" {
		t.Fatalf("out = %v, want [a]", out.Memories)
	}
}

func TestParseWithRepair_StillInvalid(t *testing.T) {
	var out map[string]interface{}
	firstErrText := ""
	_, err := ParseWithRepair(context.Background(), "test", `not json`, &out,
		func(_ context.Context, _ string, parseErr error) (string, error) {
			firstErrText = parseErr.Error()
			return `still not json`, nil
		})
	if err == nil {
		t.Fatal("修复后仍非法时应返回错误")
	}
	if firstErrText == "" || err.Error() != firstErrText {
		t.Fatalf("应返回首次解析错误（便于定位原始输出），got %v", err)
	}
}

func TestParseWithRepair_RepairCallFails(t *testing.T) {
	var out map[string]interface{}
	repairErr := errors.New("模型超时")
	_, err := ParseWithRepair(context.Background(), "test", `not json`, &out,
		func(context.Context, string, error) (string, error) {
			return "", repairErr
		})
	if err == nil {
		t.Fatal("修复调用失败时应返回错误")
	}
	if errors.Is(err, repairErr) {
		t.Fatal("不应把修复调用的错误当成解析错误返回，业务需要感知原始解析失败")
	}
}

func TestParseWithRepair_NilRepair(t *testing.T) {
	var out map[string]interface{}
	if _, err := ParseWithRepair(context.Background(), "test", `{}`, &out, nil); err != nil {
		t.Fatalf("合法 JSON 不应失败: %v", err)
	}
	if _, err := ParseWithRepair(context.Background(), "test", `bad`, &out, nil); err == nil {
		t.Fatal("非法 JSON 且无修复函数时应返回错误")
	}
}

func TestRepairPrompt_ContainsErrorAndSchema(t *testing.T) {
	prompt := RepairPrompt(`{"a":1,}`, errors.New("invalid character '}'"), `{"a":1}`)
	for _, want := range []string{"invalid character", `{"a":1}`, "只输出合法 JSON", "原始输出"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("修复提示缺少 %q: %s", want, prompt)
		}
	}
}

func TestRepairPrompt_TruncatesLongOutput(t *testing.T) {
	raw := strings.Repeat("头", 6000) + strings.Repeat("尾", 6000)
	prompt := RepairPrompt(raw, errors.New("bad"), "")
	if !strings.Contains(prompt, "中间省略") {
		t.Fatal("超长输出应被截断")
	}
	if utf8.RuneCountInString(prompt) > repairRawLimit+2000 {
		t.Fatalf("修复提示过长：%d 字符", utf8.RuneCountInString(prompt))
	}
}

func TestTruncateMiddle_ShortStringUnchanged(t *testing.T) {
	if got := truncateMiddle("short", 100); got != "short" {
		t.Fatalf("短字符串不应截断，got %q", got)
	}
}
