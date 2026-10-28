package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"
	"time"

	"Logos/internal/service/platform/billing/dao"
	"Logos/internal/service/platform/billing/model"
	"Logos/pkg/logger"
	"Logos/pkg/outbox"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// stubBillingRepo 只实现退款与支付单流程会用到的方法，其余通过嵌入 nil 接口在误调用时 panic。
type stubBillingRepo struct {
	dao.BillingRepository
	original      *model.Transaction
	refunded      float64
	account       *model.Account
	createdTx     *model.Transaction
	order         *model.PaymentOrder
	createdOrder  *model.PaymentOrder
	markPaidCalls int
	expiredCalls  int

	// 批量回收
	reapCalls  int
	reapBefore time.Time
	reapLimit  int
	reapCount  int64
	reapErr    error
}

func (s *stubBillingRepo) WithTransaction(ctx context.Context, fn func(dao.BillingRepository) error) error {
	return fn(s)
}

func (s *stubBillingRepo) GetTransaction(ctx context.Context, id string) (*model.Transaction, error) {
	return s.original, nil
}

func (s *stubBillingRepo) SumRefundedAmount(ctx context.Context, userID, originalTxID string) (float64, error) {
	return s.refunded, nil
}

func (s *stubBillingRepo) GetAccountByUserID(ctx context.Context, userID string) (*model.Account, error) {
	return s.account, nil
}

func (s *stubBillingRepo) UpdateAccount(ctx context.Context, account *model.Account) error {
	return nil
}

func (s *stubBillingRepo) CreateTransaction(ctx context.Context, tx *model.Transaction) error {
	s.createdTx = tx
	return nil
}

func (s *stubBillingRepo) DB() *gorm.DB { return nil }

func (s *stubBillingRepo) CreatePaymentOrder(ctx context.Context, order *model.PaymentOrder) error {
	s.createdOrder = order
	s.order = order
	return nil
}

func (s *stubBillingRepo) GetPaymentOrder(ctx context.Context, id string) (*model.PaymentOrder, error) {
	if s.order != nil && s.order.ID == id {
		return s.order, nil
	}
	return nil, nil
}

func (s *stubBillingRepo) MarkPaymentOrderPaid(ctx context.Context, id, tradeNo, nonce string, paidAt time.Time) (bool, error) {
	if s.order == nil || s.order.ID != id || s.order.Status != 0 {
		return false, nil
	}
	s.order.Status = 1
	s.order.TradeNo = tradeNo
	s.order.PaidAt = &paidAt
	s.markPaidCalls++
	return true, nil
}

func (s *stubBillingRepo) MarkPaymentOrderExpired(ctx context.Context, id string, now time.Time) (bool, error) {
	if s.order == nil || s.order.ID != id || s.order.Status != 0 {
		return false, nil
	}
	s.order.Status = 3
	s.expiredCalls++
	return true, nil
}

func (s *stubBillingRepo) ExpirePendingOrders(ctx context.Context, before time.Time, limit int) (int64, error) {
	s.reapCalls++
	s.reapBefore = before
	s.reapLimit = limit
	return s.reapCount, s.reapErr
}

type stubOutboxRepo struct {
	outbox.OutboxRepository
}

func (s *stubOutboxRepo) SaveWithTx(ctx context.Context, tx *gorm.DB, topic, key string, value interface{}) error {
	return nil
}

func TestRefund_RejectsInvalidRequests(t *testing.T) {
	logger.SetLogger(zap.NewNop())

	cases := []struct {
		name     string
		userID   string
		txID     string
		amount   float64
		original *model.Transaction
		refunded float64
	}{
		{
			name:     "缺少原始交易ID",
			userID:   "alice",
			txID:     "",
			amount:   1,
			original: &model.Transaction{ID: "tx-1", UserID: "alice", Type: 3, Status: 2, Amount: 10},
		},
		{
			name:     "原始交易不存在",
			userID:   "alice",
			txID:     "tx-missing",
			amount:   1,
			original: nil,
		},
		{
			name:     "对他人交易发起退款",
			userID:   "alice",
			txID:     "tx-1",
			amount:   1,
			original: &model.Transaction{ID: "tx-1", UserID: "bob", Type: 3, Status: 2, Amount: 10},
		},
		{
			name:     "非消费交易不可退款",
			userID:   "alice",
			txID:     "tx-1",
			amount:   1,
			original: &model.Transaction{ID: "tx-1", UserID: "alice", Type: 1, Status: 2, Amount: 10},
		},
		{
			name:     "未成功的消费交易不可退款",
			userID:   "alice",
			txID:     "tx-1",
			amount:   1,
			original: &model.Transaction{ID: "tx-1", UserID: "alice", Type: 3, Status: 1, Amount: 10},
		},
		{
			name:     "累计退款超过原交易金额",
			userID:   "alice",
			txID:     "tx-1",
			amount:   3,
			original: &model.Transaction{ID: "tx-1", UserID: "alice", Type: 3, Status: 2, Amount: 10},
			refunded: 8,
		},
		{
			name:     "退款金额非正数",
			userID:   "alice",
			txID:     "tx-1",
			amount:   0,
			original: &model.Transaction{ID: "tx-1", UserID: "alice", Type: 3, Status: 2, Amount: 10},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc := NewBillingService(&stubBillingRepo{original: c.original, refunded: c.refunded}, nil, "")
			if _, err := svc.Refund(context.Background(), c.userID, c.txID, c.amount, "test", nil); err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

func TestRefund_SuccessWithinOriginalAmount(t *testing.T) {
	logger.SetLogger(zap.NewNop())

	account := &model.Account{ID: "acc-1", UserID: "alice", Balance: 0}
	repo := &stubBillingRepo{
		original: &model.Transaction{ID: "tx-1", UserID: "alice", Type: 3, Status: 2, Amount: 10},
		refunded: 4,
		account:  account,
	}
	svc := NewBillingService(repo, &stubOutboxRepo{}, "")

	tx, err := svc.Refund(context.Background(), "alice", "tx-1", 3, "重复扣费", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tx == nil || tx.Type != 4 || tx.Amount != 3 {
		t.Fatalf("unexpected refund transaction: %+v", tx)
	}
	if tx.Metadata["original_transaction_id"] != "tx-1" {
		t.Errorf("original_transaction_id = %q, want tx-1", tx.Metadata["original_transaction_id"])
	}
	if account.Balance != 3 {
		t.Errorf("balance = %v, want 3", account.Balance)
	}
	if repo.createdTx != tx {
		t.Error("退款交易应落库")
	}
}

func signPayment(secret, orderID, tradeNo string, amount float64, ts int64, nonce string) string {
	payload := fmt.Sprintf("order_id=%s&trade_no=%s&amount=%.2f&timestamp=%d&nonce=%s",
		orderID, tradeNo, amount, ts, nonce)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

func TestCreatePaymentOrder_RejectsInvalidRequests(t *testing.T) {
	logger.SetLogger(zap.NewNop())

	cases := []struct {
		name   string
		userID string
		amount float64
		method string
	}{
		{name: "缺少用户", userID: "", amount: 10, method: "alipay"},
		{name: "金额为0", userID: "alice", amount: 0, method: "alipay"},
		{name: "金额为负", userID: "alice", amount: -1, method: "alipay"},
		{name: "金额超上限", userID: "alice", amount: 100001, method: "alipay"},
		{name: "支付方式不在白名单", userID: "alice", amount: 10, method: "bitcoin"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc := NewBillingService(&stubBillingRepo{}, &stubOutboxRepo{}, "secret")
			if _, err := svc.CreatePaymentOrder(context.Background(), c.userID, c.amount, c.method, nil); err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

func TestCreatePaymentOrder_Success(t *testing.T) {
	logger.SetLogger(zap.NewNop())

	repo := &stubBillingRepo{}
	svc := NewBillingService(repo, &stubOutboxRepo{}, "secret")

	order, err := svc.CreatePaymentOrder(context.Background(), "alice", 12.5, "Alipay", map[string]string{"channel": "web"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if order.Status != 0 {
		t.Errorf("status = %d, want 0 (pending)", order.Status)
	}
	if order.PaymentMethod != "alipay" {
		t.Errorf("payment method = %q, want normalized alipay", order.PaymentMethod)
	}
	if !order.ExpiresAt.After(time.Now()) {
		t.Error("支付单应有过期时间")
	}
	if repo.createdOrder != order {
		t.Error("支付单应落库")
	}
}

func TestGetPaymentOrder_RejectsOtherUsersOrder(t *testing.T) {
	logger.SetLogger(zap.NewNop())

	repo := &stubBillingRepo{order: &model.PaymentOrder{ID: "order-1", UserID: "bob", Amount: 10, Status: 0}}
	svc := NewBillingService(repo, &stubOutboxRepo{}, "secret")

	if _, err := svc.GetPaymentOrder(context.Background(), "alice", "order-1"); !errors.Is(err, ErrPaymentOrderNotFound) {
		t.Fatalf("expected ErrPaymentOrderNotFound, got %v", err)
	}
	if _, err := svc.GetPaymentOrder(context.Background(), "bob", "order-1"); err != nil {
		t.Fatalf("unexpected error for owner: %v", err)
	}
}

func TestConfirmPayment_RejectsInvalidCallbacks(t *testing.T) {
	logger.SetLogger(zap.NewNop())

	const secret = "test-secret"
	now := time.Now().Unix()
	validOrder := func() *model.PaymentOrder {
		return &model.PaymentOrder{
			ID:        "order-1",
			UserID:    "alice",
			Amount:    10,
			Status:    0,
			ExpiresAt: time.Now().Add(10 * time.Minute),
		}
	}

	cases := []struct {
		name    string
		secret  string
		order   *model.PaymentOrder
		build   func() PaymentCallback
		wantErr error
	}{
		{
			name:    "回调密钥未配置",
			secret:  "",
			order:   validOrder(),
			build:   func() PaymentCallback { return callbackWithSignature(secret, validOrder(), 10, now, "n1") },
			wantErr: ErrPaymentNotConfigured,
		},
		{
			name:   "签名错误",
			secret: secret,
			order:  validOrder(),
			build: func() PaymentCallback {
				cb := callbackWithSignature(secret, validOrder(), 10, now, "n1")
				cb.Signature = "deadbeef"
				return cb
			},
			wantErr: ErrPaymentSignature,
		},
		{
			name:    "时间戳超出窗口",
			secret:  secret,
			order:   validOrder(),
			build:   func() PaymentCallback { return callbackWithSignature(secret, validOrder(), 10, now-600, "n1") },
			wantErr: ErrPaymentTimestamp,
		},
		{
			name:    "订单不存在",
			secret:  secret,
			order:   nil,
			build:   func() PaymentCallback { return callbackWithSignature(secret, validOrder(), 10, now, "n1") },
			wantErr: ErrPaymentOrderNotFound,
		},
		{
			name:   "回调金额与订单不一致",
			secret: secret,
			order:  validOrder(),
			build: func() PaymentCallback {
				return callbackWithSignature(secret, validOrder(), 100, now, "n1")
			},
			wantErr: ErrPaymentAmountMismatch,
		},
		{
			name:   "订单已过期",
			secret: secret,
			order: func() *model.PaymentOrder {
				o := validOrder()
				o.ExpiresAt = time.Now().Add(-time.Minute)
				return o
			}(),
			build:   func() PaymentCallback { return callbackWithSignature(secret, validOrder(), 10, now, "n1") },
			wantErr: ErrPaymentOrderExpired,
		},
		{
			name:   "订单已关闭",
			secret: secret,
			order: func() *model.PaymentOrder {
				o := validOrder()
				o.Status = 2
				return o
			}(),
			build:   func() PaymentCallback { return callbackWithSignature(secret, validOrder(), 10, now, "n1") },
			wantErr: ErrPaymentOrderClosed,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			repo := &stubBillingRepo{order: c.order}
			svc := NewBillingService(repo, &stubOutboxRepo{}, c.secret)
			if _, err := svc.ConfirmPayment(context.Background(), c.build()); !errors.Is(err, c.wantErr) {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
			if repo.markPaidCalls != 0 {
				t.Error("校验失败的重复回调不应触发入账")
			}
		})
	}
}

func TestConfirmPayment_CreditsOnlyOnce(t *testing.T) {
	logger.SetLogger(zap.NewNop())

	const secret = "test-secret"
	order := &model.PaymentOrder{
		ID:        "order-1",
		UserID:    "alice",
		Amount:    10,
		Status:    0,
		ExpiresAt: time.Now().Add(10 * time.Minute),
	}
	account := &model.Account{ID: "acc-1", UserID: "alice", Balance: 0}
	repo := &stubBillingRepo{order: order, account: account}
	svc := NewBillingService(repo, &stubOutboxRepo{}, secret)

	cb := callbackWithSignature(secret, order, 10, time.Now().Unix(), "nonce-1")

	paid, err := svc.ConfirmPayment(context.Background(), cb)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if paid.Status != 1 || paid.TradeNo != cb.TradeNo {
		t.Fatalf("unexpected paid order: %+v", paid)
	}
	if account.Balance != 10 {
		t.Fatalf("balance = %v, want 10", account.Balance)
	}
	if repo.createdTx == nil || repo.createdTx.Metadata["payment_order_id"] != "order-1" {
		t.Fatalf("入账交易应记录 payment_order_id: %+v", repo.createdTx)
	}

	// 重复回调：返回成功但不再入账
	again, err := svc.ConfirmPayment(context.Background(), cb)
	if err != nil {
		t.Fatalf("duplicate callback should be idempotent, got error: %v", err)
	}
	if again.Status != 1 {
		t.Fatalf("unexpected status after duplicate: %d", again.Status)
	}
	if account.Balance != 10 {
		t.Fatalf("duplicate callback credited twice, balance = %v", account.Balance)
	}
	if repo.markPaidCalls != 1 {
		t.Fatalf("MarkPaymentOrderPaid called %d times, want 1", repo.markPaidCalls)
	}
}

func TestPaymentOrderExpiredIsRecycled(t *testing.T) {
	logger.SetLogger(zap.NewNop())

	order := &model.PaymentOrder{
		ID:        "order-expired",
		UserID:    "alice",
		Amount:    10,
		Status:    0,
		ExpiresAt: time.Now().Add(-time.Minute),
	}
	repo := &stubBillingRepo{order: order}
	svc := NewBillingService(repo, &stubOutboxRepo{}, "secret")

	got, err := svc.GetPaymentOrder(context.Background(), "alice", order.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Status != 3 {
		t.Fatalf("status = %d, want 3 (expired)", got.Status)
	}
	if repo.expiredCalls != 1 {
		t.Fatalf("expiredCalls = %d, want 1", repo.expiredCalls)
	}

	// 对过期且仍为 pending 的订单回调：拒绝入账并回收订单
	order.Status = 0
	cb := callbackWithSignature("secret", order, 10, time.Now().Unix(), "nonce-expired")
	if _, err := svc.ConfirmPayment(context.Background(), cb); !errors.Is(err, ErrPaymentOrderExpired) {
		t.Fatalf("err = %v, want ErrPaymentOrderExpired", err)
	}
	if repo.markPaidCalls != 0 {
		t.Error("过期订单不应入账")
	}
	if repo.expiredCalls != 2 {
		t.Fatalf("expiredCalls = %d, want 2", repo.expiredCalls)
	}
}

func callbackWithSignature(secret string, order *model.PaymentOrder, amount float64, ts int64, nonce string) PaymentCallback {
	return PaymentCallback{
		OrderID:   order.ID,
		TradeNo:   "trade-1",
		Amount:    amount,
		Timestamp: ts,
		Nonce:     nonce,
		Signature: signPayment(secret, order.ID, "trade-1", amount, ts, nonce),
	}
}
