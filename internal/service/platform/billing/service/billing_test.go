package service

import (
	"context"
	"testing"

	"Logos/internal/service/platform/billing/dao"
	"Logos/internal/service/platform/billing/model"
	"Logos/pkg/logger"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// stubBillingRepo 只实现退款校验会用到的方法，其余通过嵌入 nil 接口在误调用时 panic。
type stubBillingRepo struct {
	dao.BillingRepository
	original *model.Transaction
	refunded float64
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

func (s *stubBillingRepo) DB() *gorm.DB { return nil }

func TestRefund_RejectsInvalidRequests(t *testing.T) {
	logger.SetLogger(zap.NewNop())

	cases := []struct {
		name        string
		userID      string
		txID        string
		amount      float64
		original    *model.Transaction
		refunded    float64
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
			svc := NewBillingService(&stubBillingRepo{original: c.original, refunded: c.refunded}, nil)
			if _, err := svc.Refund(context.Background(), c.userID, c.txID, c.amount, "test", nil); err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}
