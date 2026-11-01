package tools

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestMCPToolInputUnmarshal_MixedTypes 模型给的参数很少严格匹配 schema：
// 数字/布尔/对象必须被保留，不能因为「解析失败」就悄悄丢掉整个 params。
func TestMCPToolInputUnmarshal_MixedTypes(t *testing.T) {
	data := []byte(`{"tool_name":"search","params":{"q":"golang","limit":10,"exact":true,"filters":{"lang":"go"},"none":null}}`)

	var in MCPToolInput
	if err := json.Unmarshal(data, &in); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if in.ToolName != "search" {
		t.Fatalf("tool_name = %q", in.ToolName)
	}

	want := map[string]string{
		"q":       "golang",
		"limit":   "10",
		"exact":   "true",
		"filters": `{"lang":"go"}`,
	}
	if len(in.Params) != len(want) {
		t.Fatalf("params = %v, want %v", in.Params, want)
	}
	for k, v := range want {
		if in.Params[k] != v {
			t.Fatalf("params[%q] = %q, want %q", k, in.Params[k], v)
		}
	}
	if _, ok := in.Params["none"]; ok {
		t.Fatalf("null 不应被当成参数传入: %v", in.Params)
	}
}

// TestMCPToolInputUnmarshal_DoubleEncoded 兼容「params 被序列化成一个字符串」的写法。
func TestMCPToolInputUnmarshal_DoubleEncoded(t *testing.T) {
	data := []byte(`{"tool_name":"search","params":"{\"q\":\"x\",\"n\":3}"}`)

	var in MCPToolInput
	if err := json.Unmarshal(data, &in); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if in.Params["q"] != "x" || in.Params["n"] != "3" {
		t.Fatalf("params = %v", in.Params)
	}
}

// TestMCPToolInputUnmarshal_NoParams 缺省 params 必须是空 map（不是 nil，避免下游写入 panic）。
func TestMCPToolInputUnmarshal_NoParams(t *testing.T) {
	var in MCPToolInput
	if err := json.Unmarshal([]byte(`{"tool_name":"t"}`), &in); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if in.Params == nil || len(in.Params) != 0 {
		t.Fatalf("params = %v, want empty non-nil map", in.Params)
	}
}

// TestToolNameSet_Unique 重名必须消解，且不能因为追加序号而超长。
func TestToolNameSet_Unique(t *testing.T) {
	names := toolNameSet{}

	if got := names.unique("search"); got != "search" {
		t.Fatalf("first = %q", got)
	}
	if got := names.unique("search"); got != "search_2" {
		t.Fatalf("second = %q", got)
	}
	if got := names.unique("search"); got != "search_3" {
		t.Fatalf("third = %q", got)
	}
}

// TestToolNameSet_TruncatesLongNames 超长名截断到 64 字符，截断后的冲突仍要被消解。
func TestToolNameSet_TruncatesLongNames(t *testing.T) {
	long := "ext_mcp_" + strings.Repeat("超长服务名", 20) // 远超 64 字符
	names := toolNameSet{}

	first := names.unique(long)
	if n := utf8.RuneCountInString(first); n > maxToolNameRunes {
		t.Fatalf("首个名字长度 %d 超过上限 %d", n, maxToolNameRunes)
	}

	second := names.unique(long)
	if first == second {
		t.Fatalf("截断后重名未被消解: %q", first)
	}
	if n := utf8.RuneCountInString(second); n > maxToolNameRunes {
		t.Fatalf("带序号的名字长度 %d 超过上限 %d: %q", n, maxToolNameRunes, second)
	}
	if !strings.HasSuffix(second, "_2") {
		t.Fatalf("second = %q, want _2 后缀", second)
	}
}

// TestExternalToolBaseName sanitize 后空名要有兜底，避免生成 "ext_mcp__"。
func TestExternalToolBaseName(t *testing.T) {
	if got := externalToolBaseName("", ""); got != "ext_mcp_svc_tool" {
		t.Fatalf("got %q", got)
	}
	got := externalToolBaseName("搜索 AI", "web_search")
	if !strings.HasPrefix(got, "ext_mcp_") || !strings.Contains(got, "web_search") {
		t.Fatalf("got %q", got)
	}
}
