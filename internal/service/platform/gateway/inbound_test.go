package gateway

import (
	"strings"
	"testing"
)

func TestValidateRequestID(t *testing.T) {
	cases := []struct {
		name string
		id   string
		ok   bool
	}{
		{"空值允许（非幂等帧可不带）", "", true},
		{"正常 UUID", "9f2b6c34-4a1b-4a8e-9f1c-0d2e3f4a5b6c", true},
		{"恰好 64 字节", strings.Repeat("a", MaxRequestIDBytes), true},
		{"超过 64 字节", strings.Repeat("a", MaxRequestIDBytes+1), false},
		{"包含空格", "abc def", false},
		{"包含换行（日志注入）", "abc\ndef", false},
		{"包含制表符", "abc\tdef", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := ValidateRequestID(tc.id)
			if tc.ok && v != nil {
				t.Fatalf("期望通过，实际拒绝: %v", v)
			}
			if !tc.ok && v == nil {
				t.Fatal("期望拒绝，实际通过")
			}
			if v != nil && v.Error() == "" {
				t.Fatal("拒绝原因不能为空")
			}
		})
	}
}

func TestValidateChatID(t *testing.T) {
	cases := []struct {
		name   string
		chatID string
		ok     bool
	}{
		{"正常 UUID", "c1f2e3d4-0000-0000-0000-000000000001", true},
		{"恰好 128 字节", strings.Repeat("a", MaxChatIDBytes), true},
		{"空值", "", false},
		{"超过 128 字节", strings.Repeat("a", MaxChatIDBytes+1), false},
		{"包含空白", "chat 1", false},
		{"包含全角空格", "chat\u3000 1", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := ValidateChatID(tc.chatID)
			if tc.ok && v != nil {
				t.Fatalf("期望通过，实际拒绝: %v", v)
			}
			if !tc.ok && v == nil {
				t.Fatal("期望拒绝，实际通过")
			}
		})
	}
}

func TestValidateContent(t *testing.T) {
	cases := []struct {
		name    string
		content string
		ok      bool
	}{
		{"普通中文", "你好，世界", true},
		{"恰好上限", strings.Repeat("a", MaxContentBytes), true},
		{"空内容", "", false},
		{"超过上限", strings.Repeat("a", MaxContentBytes+1), false},
		// 按字节而非字符计数：3 字节中文 × (MaxContentBytes/3 + 1) 必然超限
		{"多字节内容按字节计", strings.Repeat("你", MaxContentBytes/3+1), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := ValidateContent(tc.content)
			if tc.ok && v != nil {
				t.Fatalf("期望通过，实际拒绝: %v", v)
			}
			if !tc.ok && v == nil {
				t.Fatal("期望拒绝，实际通过")
			}
		})
	}
}

func TestValidateMessageIDs(t *testing.T) {
	valid := []string{"m1", "m2"}
	if v := ValidateMessageIDs(valid); v != nil {
		t.Fatalf("正常列表被拒绝: %v", v)
	}
	if v := ValidateMessageIDs(nil); v == nil {
		t.Fatal("空列表应拒绝")
	}
	if v := ValidateMessageIDs([]string{}); v == nil {
		t.Fatal("空切片应拒绝")
	}
	if v := ValidateMessageIDs([]string{"m1", ""}); v == nil {
		t.Fatal("包含空 ID 应拒绝")
	}
	if v := ValidateMessageIDs([]string{strings.Repeat("a", MaxMessageIDBytes+1)}); v == nil {
		t.Fatal("超长 ID 应拒绝")
	}

	tooMany := make([]string, MaxMessageIDsPerReceipt+1)
	for i := range tooMany {
		tooMany[i] = "m"
	}
	if v := ValidateMessageIDs(tooMany); v == nil {
		t.Fatal("超过数量上限应拒绝")
	}
	if v := ValidateMessageIDs(tooMany[:MaxMessageIDsPerReceipt]); v != nil {
		t.Fatalf("恰好达到上限应通过，实际拒绝: %v", v)
	}
}

func TestValidateMentionIDs(t *testing.T) {
	if v := ValidateMentionIDs(nil); v != nil {
		t.Fatalf("未 @ 任何人应通过: %v", v)
	}
	if v := ValidateMentionIDs([]string{"u1", "u2"}); v != nil {
		t.Fatalf("正常 @ 列表被拒绝: %v", v)
	}
	if v := ValidateMentionIDs([]string{""}); v == nil {
		t.Fatal("空用户 ID 应拒绝")
	}
	if v := ValidateMentionIDs([]string{strings.Repeat("u", MaxChatIDBytes+1)}); v == nil {
		t.Fatal("超长用户 ID 应拒绝")
	}

	tooMany := make([]string, MaxMentionIDs+1)
	for i := range tooMany {
		tooMany[i] = "u"
	}
	if v := ValidateMentionIDs(tooMany); v == nil {
		t.Fatal("超过 @ 数量上限应拒绝")
	}
	if v := ValidateMentionIDs(tooMany[:MaxMentionIDs]); v != nil {
		t.Fatalf("恰好达到上限应通过，实际拒绝: %v", v)
	}
}

// TestViolationMessage 校验错误信息包含字段名与原因，便于拼进错误帧和日志聚合。
func TestViolationMessage(t *testing.T) {
	v := &Violation{Field: "chat_id", Reason: "不能为空"}
	if got := v.Error(); got != "chat_id: 不能为空" {
		t.Fatalf("Violation.Error() = %q", got)
	}
	var nilV *Violation
	if got := nilV.Error(); got != "" {
		t.Fatalf("nil Violation.Error() = %q, want empty", got)
	}
}
