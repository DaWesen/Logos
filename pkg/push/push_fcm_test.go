package push

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

type fcmRoundTripFunc func(*http.Request) (*http.Response, error)

func (f fcmRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type fcmCapture struct {
	req *http.Request
}

// newFCMTestProvider 用自定义 Transport 截获发往 FCM 的请求。
// 这样能在不联网的前提下断言真实请求头，也能把任意响应体喂给解析逻辑。
func newFCMTestProvider(t *testing.T, statusCode int, body string) (*FCMProvider, *fcmCapture) {
	t.Helper()
	capture := &fcmCapture{}
	provider := NewFCMProvider(FCMConfig{ServerKey: "test-server-key"})
	provider.client = &http.Client{
		Transport: fcmRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			capture.req = req
			return &http.Response{
				StatusCode: statusCode,
				Status:     fmt.Sprintf("%d %s", statusCode, http.StatusText(statusCode)),
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(body)),
				Request:    req,
			}, nil
		}),
	}
	return provider, capture
}

func TestFCMProvider_SendSuccess(t *testing.T) {
	provider, capture := newFCMTestProvider(t, http.StatusOK,
		`{"multicast_id":1,"success":1,"failure":0,"results":[{"message_id":"0:1"}]}`)

	result, err := provider.Send(context.Background(), &PushMessage{
		UserID: "u1", Token: "tok", Title: "t", Body: "b",
	})
	if err != nil {
		t.Fatalf("成功响应不应返回错误: %v", err)
	}
	if !result.Success {
		t.Fatalf("success=1 应判定为发送成功: %+v", result)
	}
	if got := capture.req.Header.Get("Authorization"); got != "key=test-server-key" {
		t.Fatalf("Authorization 头错误: %q", got)
	}
	if got := capture.req.URL.String(); got != "https://fcm.googleapis.com/fcm/send" {
		t.Fatalf("请求地址错误: %q", got)
	}
}

func TestFCMProvider_InvalidTokenIsUnregisterable(t *testing.T) {
	provider, _ := newFCMTestProvider(t, http.StatusOK,
		`{"success":0,"failure":1,"results":[{"error":"NotRegistered"}]}`)

	result, err := provider.Send(context.Background(), &PushMessage{UserID: "u1", Token: "dead"})
	if err == nil {
		t.Fatal("单条失败应返回错误")
	}
	if result.Success {
		t.Fatalf("success=0 不应判定为成功: %+v", result)
	}
	if result.Failure.Kind != PushFailurePermanent || !result.Failure.ShouldUnregister() {
		t.Fatalf("NotRegistered 应触发令牌注销: %+v", result.Failure)
	}
}

func TestFCMProvider_TruncatedBodyIsContractErrorNotVendorRejection(t *testing.T) {
	// HTTP 200 但响应体被截断（代理超时、网关改写）。真实原因是「我们没解析出来」，
	// 不能编一个 "message rejected" 冒充供应商拒收。
	provider, _ := newFCMTestProvider(t, http.StatusOK, `{"success":1,"results":[{"message_`)

	result, err := provider.Send(context.Background(), &PushMessage{UserID: "u1", Token: "tok"})
	if err == nil {
		t.Fatal("解码失败应返回错误")
	}
	if result.Success {
		t.Fatalf("解析失败不应判定为成功: %+v", result)
	}
	if result.Reason == "message rejected" {
		t.Fatalf("不得把解析失败伪装成供应商拒收: %+v", result)
	}
	if !strings.Contains(result.Reason, "invalid FCM response body") {
		t.Fatalf("Reason 应指明响应体非法，got %q", result.Reason)
	}
	if result.Failure.Kind != PushFailureUnknown {
		t.Fatalf("解析失败应归类为 unknown，got %s", result.Failure.Kind)
	}
	if result.Failure.ShouldUnregister() {
		t.Fatal("解析失败不能注销令牌")
	}
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("原始解码错误应被包装保留，got %v", err)
	}
}

func TestFCMProvider_EmptyCountersIsContractError(t *testing.T) {
	provider, _ := newFCMTestProvider(t, http.StatusOK, `{"success":0,"failure":0}`)

	result, err := provider.Send(context.Background(), &PushMessage{UserID: "u1", Token: "tok"})
	if err == nil {
		t.Fatal("空壳响应应返回错误")
	}
	if result.Reason != "empty FCM response" {
		t.Fatalf("Reason 应区分空壳响应，got %q", result.Reason)
	}
	if result.Failure.Kind != PushFailureUnknown || result.Failure.ShouldUnregister() {
		t.Fatalf("空壳响应应归类为 unknown 且不注销令牌: %+v", result.Failure)
	}
}

func TestFCMProvider_VendorRetryableReason(t *testing.T) {
	provider, _ := newFCMTestProvider(t, http.StatusOK,
		`{"success":0,"failure":1,"results":[{"error":"Unavailable"}]}`)

	result, _ := provider.Send(context.Background(), &PushMessage{UserID: "u1", Token: "tok"})
	if result.Failure.Kind != PushFailureRetryable {
		t.Fatalf("Unavailable 应可重试，got %s", result.Failure.Kind)
	}
	if result.Failure.ShouldUnregister() {
		t.Fatal("临时不可用不得注销令牌")
	}
}
