package gateway

import "testing"

// TestErrorCodesUnique 错误码必须互不重复：客户端按数字分支，
// 两个语义共用同一个值会让「令牌失效」和「参数错误」无法区分。
func TestErrorCodesUnique(t *testing.T) {
	codes := []int32{
		CodeOK,
		CodeBadRequest,
		CodeUnauthorized,
		CodeForbidden,
		CodeNotFound,
		CodeTooManyRequests,
		CodeInternal,
	}
	seen := make(map[int32]int, len(codes))
	for i, code := range codes {
		if prev, ok := seen[code]; ok {
			t.Fatalf("错误码 %d 重复：索引 %d 与 %d", code, prev, i)
		}
		seen[code] = i
	}
}

// TestDefaultErrorMessage 每个预定义错误码都有默认文案，未知码有兜底文案，
// 确保错误帧永远不会出现空 message。
func TestDefaultErrorMessage(t *testing.T) {
	cases := []int32{
		CodeOK,
		CodeBadRequest,
		CodeUnauthorized,
		CodeForbidden,
		CodeNotFound,
		CodeTooManyRequests,
		CodeInternal,
	}
	for _, code := range cases {
		if msg := DefaultErrorMessage(code); msg == "" {
			t.Fatalf("错误码 %d 缺少默认文案", code)
		}
	}
	if msg := DefaultErrorMessage(9999); msg == "" {
		t.Fatal("未知错误码应有兜底文案")
	}
}

// TestErrorMessagePrefersSpecific 具体文案优先；空文案回落到默认文案。
func TestErrorMessagePrefersSpecific(t *testing.T) {
	if got := ErrorMessage(CodeBadRequest, "request_id 过长（最多 64 字节）"); got != "request_id 过长（最多 64 字节）" {
		t.Fatalf("应保留具体文案，实际 %q", got)
	}
	if got := ErrorMessage(CodeTooManyRequests, ""); got != DefaultErrorMessage(CodeTooManyRequests) {
		t.Fatalf("空文案应回落到默认文案，实际 %q", got)
	}
}

// TestIsRetryable 只有限流与内部错误可重试：
// 参数/身份类错误原样重发只会放大无效流量。
func TestIsRetryable(t *testing.T) {
	retryable := []int32{CodeTooManyRequests, CodeInternal}
	for _, code := range retryable {
		if !IsRetryable(code) {
			t.Errorf("错误码 %d 应可重试", code)
		}
	}
	nonRetryable := []int32{CodeBadRequest, CodeUnauthorized, CodeForbidden, CodeNotFound, CodeOK, 9999}
	for _, code := range nonRetryable {
		if IsRetryable(code) {
			t.Errorf("错误码 %d 不应可重试", code)
		}
	}
}
