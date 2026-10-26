package strutil

import (
	"strings"
	"testing"
)

func TestExtractJSONValue(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"纯对象", `{"result":"passed"}`, `{"result":"passed"}`},
		{"纯数组", `[{"id":"1"},{"id":"2"}]`, `[{"id":"1"},{"id":"2"}]`},
		{"json 围栏对象", "```json\n{\"a\":1}\n```", `{"a":1}`},
		{"裸围栏数组", "```\n[1,2,3]\n```", `[1,2,3]`},
		{"前后夹杂解释", "结果如下：[{\"a\":1}] 希望有帮助", `[{"a":1}]`},
		{"嵌套数组对象", `{"list":[1,{"b":[2]}]}`, `{"list":[1,{"b":[2]}]}`},
		{"字符串含括号", `{"a":"}","b":"["}`, `{"a":"}","b":"["}`},
		{"转义引号", `{"a":"he said \"}\""}`, `{"a":"he said \"}\""}`},
		{"截断兜底", `[{"a":1},`, `[{"a":1}`},
		{"无 JSON 原样返回", `抱歉，我无法回答。`, `抱歉，我无法回答。`},
		{"空串", ``, ``},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ExtractJSONValue(c.in); got != c.want {
				t.Errorf("ExtractJSONValue(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestExtractJSONValue_TruncatedFallsBackToLastCloser(t *testing.T) {
	// 括号不配对（缺少 ']'），退化为最后一个 '}' 收尾，交给 json.Unmarshal 报错
	in := `说明：[{"a":1},{"b":2}`
	got := ExtractJSONValue(in)
	if got != `[{"a":1},{"b":2}` {
		t.Errorf("got %q", got)
	}
}

func TestCleanInvalidUTF8_RemovesInvalidBytes(t *testing.T) {
	valid := "你好，世界 hello"
	invalid := "bad\xff\xfeutf8"
	got := CleanInvalidUTF8(valid + invalid + "end")
	if strings.ContainsRune(got, 0xFFFD) {
		t.Errorf("result should not contain RuneError: %q", got)
	}
	if !strings.HasPrefix(got, valid) || !strings.HasSuffix(got, "end") {
		t.Errorf("valid parts should be preserved: %q", got)
	}
}

func TestCleanInvalidUTF8_RemovesNullBytes(t *testing.T) {
	got := CleanInvalidUTF8("a\x00b")
	if got != "ab" {
		t.Errorf("CleanInvalidUTF8(\"a\\x00b\") = %q, want \"ab\"", got)
	}
}

func TestCleanInvalidUTF8_ValidInputUnchanged(t *testing.T) {
	in := "plain ASCII + 中文 + emoji 😀"
	if got := CleanInvalidUTF8(in); got != in {
		t.Errorf("valid input should be unchanged, got %q", got)
	}
}

func TestTruncateByRunes_ShortInputUnchanged(t *testing.T) {
	in := "短文本"
	if got := TruncateByRunes(in, 100); got != in {
		t.Errorf("short input should be unchanged, got %q", got)
	}
}

func TestTruncateByRunes_TruncatesLongInput(t *testing.T) {
	in := strings.Repeat("字", 100)
	got := TruncateByRunes(in, 30)
	if got == in {
		t.Fatal("long input should be truncated")
	}
	// 头尾保留 + 截断标记
	if !strings.Contains(got, "…") && !strings.Contains(got, "...") {
		// 标记为 "…(内容已截断)" 或降级为 "..."
		if !strings.Contains(got, "内容已截断") {
			t.Errorf("truncation marker missing: %q", got)
		}
	}
}

func TestTruncateByRunes_NonPositiveLimit(t *testing.T) {
	if got := TruncateByRunes("hello", 0); got != "hello" {
		t.Errorf("maxRunes=0 should return original, got %q", got)
	}
	if got := TruncateByRunes("hello", -1); got != "hello" {
		t.Errorf("maxRunes=-1 should return original, got %q", got)
	}
}

func TestTruncateByRunes_TinyLimit(t *testing.T) {
	// 可用空间不足放置标记时应降级
	got := TruncateByRunes(strings.Repeat("ab", 50), 2)
	if len([]rune(got)) > 2+len("...(内容已截断)") {
		t.Errorf("tiny limit should produce short output, got %q", got)
	}
}
