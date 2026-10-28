package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"Logos/internal/service/platform/gateway/model"
	pb "Logos/proto_gen/billing"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"
)

// fakeBillingClient 只实现被测方法，其余方法由内嵌接口兜底：
// 一旦被测 handler 走了非预期路径会直接 panic，而不是静默返回零值。
type fakeBillingClient struct {
	pb.BillingServiceClient

	createOrder func(ctx context.Context, in *pb.CreatePaymentOrderRequest) (*pb.CreatePaymentOrderResponse, error)
	getOrder    func(ctx context.Context, in *pb.GetPaymentOrderRequest) (*pb.GetPaymentOrderResponse, error)
	confirm     func(ctx context.Context, in *pb.ConfirmPaymentRequest) (*pb.ConfirmPaymentResponse, error)
}

func (f *fakeBillingClient) CreatePaymentOrder(ctx context.Context, in *pb.CreatePaymentOrderRequest, _ ...grpc.CallOption) (*pb.CreatePaymentOrderResponse, error) {
	return f.createOrder(ctx, in)
}

func (f *fakeBillingClient) GetPaymentOrder(ctx context.Context, in *pb.GetPaymentOrderRequest, _ ...grpc.CallOption) (*pb.GetPaymentOrderResponse, error) {
	return f.getOrder(ctx, in)
}

func (f *fakeBillingClient) ConfirmPayment(ctx context.Context, in *pb.ConfirmPaymentRequest, _ ...grpc.CallOption) (*pb.ConfirmPaymentResponse, error) {
	return f.confirm(ctx, in)
}

// newBillingTestRouter 注册与生产一致的支付路由；userID 为空表示未认证请求，
// 用于验证回调这种公开接口不依赖登录态。
func newBillingTestRouter(userID string, client pb.BillingServiceClient) *gin.Engine {
	gin.SetMode(gin.TestMode)

	h := &Handler{BillingClient: client}
	r := gin.New()
	api := r.Group("/api/v1")
	// 模拟认证中间件
	api.Use(func(c *gin.Context) {
		if userID != "" {
			c.Set("user_id", userID)
		}
		c.Next()
	})
	api.POST("/billing/payments", h.CreatePaymentOrder)
	api.GET("/billing/payments/:id", h.GetPaymentOrder)
	api.POST("/billing/payment-callback", h.PaymentCallback)
	return r
}

func doBillingRequest(t *testing.T, r *gin.Engine, method, path, body string) (int, model.Response) {
	t.Helper()

	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var resp model.Response
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v, body=%s", err, w.Body.String())
	}
	return w.Code, resp
}

// 请求体里的 user_id 是攻击者可控字段，必须被认证中间件写入的身份覆盖。
func TestCreatePaymentOrder_IgnoresUserIDInBody(t *testing.T) {
	var gotUserID string
	client := &fakeBillingClient{
		createOrder: func(_ context.Context, in *pb.CreatePaymentOrderRequest) (*pb.CreatePaymentOrderResponse, error) {
			gotUserID = in.GetUserId()
			return &pb.CreatePaymentOrderResponse{
				Code:    0,
				Message: "ok",
				Data: &pb.PaymentOrder{
					Id:            "order-1",
					UserId:        in.GetUserId(),
					Amount:        in.GetAmount(),
					PaymentMethod: in.GetPaymentMethod(),
					Status:        pb.PaymentOrderStatus_PAYMENT_ORDER_STATUS_PENDING,
				},
			}, nil
		},
	}
	router := newBillingTestRouter("user-1", client)

	status, resp := doBillingRequest(t, router, http.MethodPost, "/api/v1/billing/payments",
		`{"user_id":"attacker","amount":10,"payment_method":"alipay"}`)

	if status != http.StatusOK {
		t.Fatalf("HTTP 状态码 = %d, want 200", status)
	}
	if gotUserID != "user-1" {
		t.Fatalf("下游收到的 user_id = %q, want user-1", gotUserID)
	}
	if resp.Code != 0 {
		t.Fatalf("业务码 = %d, want 0", resp.Code)
	}

	data, ok := resp.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("响应 data 类型异常: %T", resp.Data)
	}
	if data["id"] != "order-1" {
		t.Fatalf("响应订单号 = %v, want order-1", data["id"])
	}
	if data["amount"] != float64(10) {
		t.Fatalf("响应金额 = %v, want 10", data["amount"])
	}
}

func TestCreatePaymentOrder_InvalidJSON(t *testing.T) {
	called := false
	client := &fakeBillingClient{
		createOrder: func(_ context.Context, _ *pb.CreatePaymentOrderRequest) (*pb.CreatePaymentOrderResponse, error) {
			called = true
			return &pb.CreatePaymentOrderResponse{Code: 0}, nil
		},
	}
	router := newBillingTestRouter("user-1", client)

	status, resp := doBillingRequest(t, router, http.MethodPost, "/api/v1/billing/payments", `{"amount":`)

	if status != http.StatusBadRequest {
		t.Fatalf("HTTP 状态码 = %d, want 400", status)
	}
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("业务码 = %d, want 400", resp.Code)
	}
	if called {
		t.Fatal("请求体非法时不应调用下游服务")
	}
}

func TestCreatePaymentOrder_Unauthenticated(t *testing.T) {
	called := false
	client := &fakeBillingClient{
		createOrder: func(_ context.Context, _ *pb.CreatePaymentOrderRequest) (*pb.CreatePaymentOrderResponse, error) {
			called = true
			return &pb.CreatePaymentOrderResponse{Code: 0}, nil
		},
	}
	router := newBillingTestRouter("", client)

	status, resp := doBillingRequest(t, router, http.MethodPost, "/api/v1/billing/payments", `{"amount":10,"payment_method":"alipay"}`)

	if status != http.StatusUnauthorized {
		t.Fatalf("HTTP 状态码 = %d, want 401", status)
	}
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("业务码 = %d, want 401", resp.Code)
	}
	if called {
		t.Fatal("未认证请求不应调用下游服务")
	}
}

// 服务端校验失败（金额越界、支付方式不在白名单）在网关层应转成 400 而不是 500。
func TestCreatePaymentOrder_BusinessRejectionIsBadRequest(t *testing.T) {
	client := &fakeBillingClient{
		createOrder: func(_ context.Context, _ *pb.CreatePaymentOrderRequest) (*pb.CreatePaymentOrderResponse, error) {
			return &pb.CreatePaymentOrderResponse{Code: 400, Message: "unsupported payment method"}, nil
		},
	}
	router := newBillingTestRouter("user-1", client)

	status, resp := doBillingRequest(t, router, http.MethodPost, "/api/v1/billing/payments",
		`{"amount":10,"payment_method":"manual"}`)

	if status != http.StatusBadRequest {
		t.Fatalf("HTTP 状态码 = %d, want 400", status)
	}
	if resp.Code != http.StatusBadRequest || resp.Message != "unsupported payment method" {
		t.Fatalf("响应 = %+v, want code=400 message=unsupported payment method", resp)
	}
}

func TestGetPaymentOrder_NotFound(t *testing.T) {
	var gotUserID, gotOrderID string
	client := &fakeBillingClient{
		getOrder: func(_ context.Context, in *pb.GetPaymentOrderRequest) (*pb.GetPaymentOrderResponse, error) {
			gotUserID = in.GetUserId()
			gotOrderID = in.GetOrderId()
			return &pb.GetPaymentOrderResponse{Code: 404, Message: "payment order not found"}, nil
		},
	}
	router := newBillingTestRouter("user-1", client)

	status, resp := doBillingRequest(t, router, http.MethodGet, "/api/v1/billing/payments/order-1", "")

	if status != http.StatusNotFound {
		t.Fatalf("HTTP 状态码 = %d, want 404", status)
	}
	if resp.Code != http.StatusNotFound {
		t.Fatalf("业务码 = %d, want 404", resp.Code)
	}
	if gotUserID != "user-1" || gotOrderID != "order-1" {
		t.Fatalf("下游收到 user_id=%q order_id=%q, want user-1/order-1", gotUserID, gotOrderID)
	}
}

// 回调是公开接口：不能要求登录态，安全性完全依赖服务端验签，字段必须原样透传给计费服务。
func TestPaymentCallback_IsPublicAndForwardsSignature(t *testing.T) {
	var got *pb.ConfirmPaymentRequest
	client := &fakeBillingClient{
		confirm: func(_ context.Context, in *pb.ConfirmPaymentRequest) (*pb.ConfirmPaymentResponse, error) {
			got = in
			return &pb.ConfirmPaymentResponse{
				Code: 0,
				Data: &pb.PaymentOrder{Id: in.GetOrderId(), Status: pb.PaymentOrderStatus_PAYMENT_ORDER_STATUS_PAID},
			}, nil
		},
	}
	router := newBillingTestRouter("", client)

	body := `{"order_id":"order-1","trade_no":"trade-1","amount":10,"timestamp":1700000000,"nonce":"nonce-1","signature":"sig-1"}`
	status, resp := doBillingRequest(t, router, http.MethodPost, "/api/v1/billing/payment-callback", body)

	if status != http.StatusOK {
		t.Fatalf("HTTP 状态码 = %d, want 200", status)
	}
	if resp.Code != 0 {
		t.Fatalf("业务码 = %d, want 0", resp.Code)
	}
	if got == nil {
		t.Fatal("未调用计费服务")
	}
	if got.GetOrderId() != "order-1" || got.GetTradeNo() != "trade-1" || got.GetNonce() != "nonce-1" || got.GetSignature() != "sig-1" {
		t.Fatalf("回调字段透传异常: %+v", got)
	}
	if got.GetAmount() != 10 || got.GetTimestamp() != 1700000000 {
		t.Fatalf("回调金额/时间戳透传异常: amount=%v timestamp=%v", got.GetAmount(), got.GetTimestamp())
	}
}

// 验签失败/金额不一致等回调拒绝必须返回 400，由渠道按重试策略处理，而不是 500。
func TestPaymentCallback_RejectionIsBadRequest(t *testing.T) {
	client := &fakeBillingClient{
		confirm: func(_ context.Context, _ *pb.ConfirmPaymentRequest) (*pb.ConfirmPaymentResponse, error) {
			return &pb.ConfirmPaymentResponse{Code: 400, Message: "invalid payment signature"}, nil
		},
	}
	router := newBillingTestRouter("", client)

	status, resp := doBillingRequest(t, router, http.MethodPost, "/api/v1/billing/payment-callback",
		`{"order_id":"order-1","trade_no":"trade-1","amount":10,"timestamp":1700000000,"nonce":"n","signature":"bad"}`)

	if status != http.StatusBadRequest {
		t.Fatalf("HTTP 状态码 = %d, want 400", status)
	}
	if resp.Code != http.StatusBadRequest || resp.Message != "invalid payment signature" {
		t.Fatalf("响应 = %+v, want code=400 message=invalid payment signature", resp)
	}
}
