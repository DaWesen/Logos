package service

import (
	"context"
	"testing"

	"Logos/internal/service/platform/billing/dao"
	"Logos/internal/service/platform/billing/model"
	"Logos/pkg/logger"
	"Logos/pkg/outbox"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// stubBillingRepo 只实现退款流程会用到的方法，其余通过嵌入 nil 接口在误调用时 panic。
type stubBillingRepo struct {
	dao.BillingRepository
	original  *model.Transaction
	refunded  float64
	account   *model.Account
	createdTx *model.Transaction
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

type stubOutboxRepo struct {
	outbox.OutboxRepository
}

func (s *stubOutboxRepo) SaveWithTx(ctx context.Context, tx *gorm.DB, topic, key string, value interface{}) error {
	return nil
}

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

func TestRefund_SuccessWithinOriginalAmount(t *testing.T) {
	logger.SetLogger(zap.NewNop())

	account := &model.Account{ID: "acc-1", UserID: "alice", Balance: 0}
	repo := &stubBillingRepo{
		original: &model.Transaction{ID: "tx-1", UserID: "alice", Type: 3, Status: 2, Amount: 10},
		refunded: 4,
		account:  account,
	}
	svc := NewBillingService(repo, &stubOutboxRepo{})

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
