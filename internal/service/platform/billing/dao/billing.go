package dao

import (
	"context"
	"errors"
	"strconv"
	"time"

	"Logos/internal/service/platform/billing/model"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// BillingRepository 计费数据仓库接口
type BillingRepository interface {
	// 账户管理
	CreateAccount(ctx context.Context, account *model.Account) error
	UpdateAccount(ctx context.Context, account *model.Account) error
	GetAccountByID(ctx context.Context, id string) (*model.Account, error)
	GetAccountByUserID(ctx context.Context, userID string) (*model.Account, error)

	// 交易管理
	CreateTransaction(ctx context.Context, tx *model.Transaction) error
	GetTransaction(ctx context.Context, id string) (*model.Transaction, error)
	ListTransactions(ctx context.Context, userID string, txType *int, startTime, endTime *time.Time, page, pageSize int) ([]*model.Transaction, int64, error)

	// 消费扣减
	ConsumeBalance(ctx context.Context, userID string, item int, amount float64, description string, metadata map[string]string) (*model.Transaction, error)

	// 用量聚合
	AggregateUsageByItem(ctx context.Context, userID string, item *int, startTime, endTime *time.Time) (map[string]float64, error)

	// 事务
	WithTransaction(ctx context.Context, fn func(txRepo BillingRepository) error) error
}

// billingRepositoryImpl 实现
type billingRepositoryImpl struct {
	db *gorm.DB
}

func NewBillingRepository(db *gorm.DB) BillingRepository {
	return &billingRepositoryImpl{db: db}
}

// 账户管理
func (r *billingRepositoryImpl) CreateAccount(ctx context.Context, account *model.Account) error {
	if account.ID == "" {
		account.ID = uuid.NewString()
	}
	return r.db.WithContext(ctx).Create(account).Error
}

func (r *billingRepositoryImpl) UpdateAccount(ctx context.Context, account *model.Account) error {
	return r.db.WithContext(ctx).Save(account).Error
}

func (r *billingRepositoryImpl) GetAccountByID(ctx context.Context, id string) (*model.Account, error) {
	var account model.Account
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&account).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &account, err
}

func (r *billingRepositoryImpl) GetAccountByUserID(ctx context.Context, userID string) (*model.Account, error) {
	var account model.Account
	err := r.db.WithContext(ctx).Where("user_id = ?", userID).First(&account).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &account, err
}

// 交易管理
func (r *billingRepositoryImpl) CreateTransaction(ctx context.Context, tx *model.Transaction) error {
	if tx.ID == "" {
		tx.ID = uuid.NewString()
	}
	return r.db.WithContext(ctx).Create(tx).Error
}

func (r *billingRepositoryImpl) GetTransaction(ctx context.Context, id string) (*model.Transaction, error) {
	var tx model.Transaction
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&tx).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &tx, err
}

func (r *billingRepositoryImpl) ListTransactions(ctx context.Context, userID string, txType *int, startTime, endTime *time.Time, page, pageSize int) ([]*model.Transaction, int64, error) {
	var txs []*model.Transaction
	var total int64
	offset := (page - 1) * pageSize

	db := r.db.WithContext(ctx).Model(&model.Transaction{})
	if userID != "" {
		db = db.Where("user_id = ?", userID)
	}
	if txType != nil {
		db = db.Where("type = ?", *txType)
	}
	if startTime != nil {
		db = db.Where("created_at >= ?", *startTime)
	}
	if endTime != nil {
		db = db.Where("created_at <= ?", *endTime)
	}

	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := db.Offset(offset).Limit(pageSize).Order("created_at DESC").Find(&txs).Error; err != nil {
		return nil, 0, err
	}
	return txs, total, nil
}

func (r *billingRepositoryImpl) ConsumeBalance(ctx context.Context, userID string, item int, amount float64, description string, metadata map[string]string) (*model.Transaction, error) {
	var tx *model.Transaction

	err := r.db.WithContext(ctx).Transaction(func(dbTx *gorm.DB) error {
		var account model.Account
		if err := dbTx.Where("user_id = ?", userID).First(&account).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				account = model.Account{
					UserID:  userID,
					Balance: 0,
					Usage:   map[string]string{},
				}
				if account.ID == "" {
					account.ID = uuid.NewString()
				}
				if err := dbTx.Create(&account).Error; err != nil {
					return err
				}
			} else {
				return err
			}
		}

		if account.Balance+account.CreditLimit < amount {
			return errors.New("insufficient balance")
		}

		balanceBefore := account.Balance
		account.Balance -= amount
		if err := dbTx.Save(&account).Error; err != nil {
			return err
		}

		tx = &model.Transaction{
			UserID:        userID,
			AccountID:     account.ID,
			Type:          3,
			Item:          item,
			Amount:        amount,
			BalanceBefore: balanceBefore,
			BalanceAfter:  account.Balance,
			Description:   description,
			Status:        2,
			Metadata:      model.JSONMap(metadata),
		}
		if tx.ID == "" {
			tx.ID = uuid.NewString()
		}
		return dbTx.Create(tx).Error
	})

	if err != nil {
		return nil, err
	}
	return tx, nil
}

func (r *billingRepositoryImpl) AggregateUsageByItem(ctx context.Context, userID string, item *int, startTime, endTime *time.Time) (map[string]float64, error) {
	type result struct {
		Item  int     `gorm:"column:item"`
		Total float64 `gorm:"column:total"`
	}
	var results []result
	db := r.db.WithContext(ctx).Model(&model.Transaction{}).
		Select("item, SUM(amount) as total").
		Where("user_id = ? AND type = ?", userID, 3)
	if item != nil {
		db = db.Where("item = ?", *item)
	}
	if startTime != nil {
		db = db.Where("created_at >= ?", *startTime)
	}
	if endTime != nil {
		db = db.Where("created_at <= ?", *endTime)
	}
	if err := db.Group("item").Find(&results).Error; err != nil {
		return nil, err
	}
	stats := make(map[string]float64)
	for _, r := range results {
		key := "item_" + strconv.Itoa(r.Item)
		stats[key] = r.Total
	}
	return stats, nil
}

func (r *billingRepositoryImpl) WithTransaction(ctx context.Context, fn func(txRepo BillingRepository) error) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		txRepo := &billingRepositoryImpl{db: tx}
		return fn(txRepo)
	})
}
