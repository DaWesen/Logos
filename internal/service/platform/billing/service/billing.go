package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"Logos/internal/service/platform/billing/dao"
	"Logos/internal/service/platform/billing/model"
	"Logos/pkg/logger"
	"Logos/pkg/mq"
)

var (
	ErrAccountNotFound  = errors.New("account not found")
	ErrInsufficientFund = errors.New("insufficient fund")
	ErrInternalServer   = errors.New("internal server error")
)

// BillingService 计费服务
type BillingService interface {
	Deposit(ctx context.Context, userID string, amount float64, paymentMethod string, metadata map[string]string) (*model.Transaction, error)
	Withdraw(ctx context.Context, userID string, amount float64, withdrawMethod string, metadata map[string]string) (*model.Transaction, error)
	GetAccount(ctx context.Context, userID string) (*model.Account, error)
	GetTransactions(ctx context.Context, userID string, txType *int, startTime, endTime *time.Time, page, pageSize int) ([]*model.Transaction, int64, error)
	GetUsageStats(ctx context.Context, userID string, item *int, startTime, endTime *time.Time) (map[string]float64, error)
	ConsumeModelCall(ctx context.Context, userID string, provider string, modelName string, tokenCount int, metadata map[string]string) (float64, error)
	ConsumeEmbedding(ctx context.Context, userID string, provider string, modelName string, tokenCount int, vectorCount int, metadata map[string]string) (float64, error)
	ConsumeStorage(ctx context.Context, userID string, storageType string, sizeBytes int64, metadata map[string]string) (float64, error)
	ConsumeBandwidth(ctx context.Context, userID string, bandwidthType string, bytes int64, metadata map[string]string) (float64, error)
	Refund(ctx context.Context, userID string, transactionID string, amount float64, reason string, metadata map[string]string) (*model.Transaction, error)
	WithTransaction(ctx context.Context, fn func(txService BillingService) error) error
}

type billingServiceImpl struct {
	repo     dao.BillingRepository
	producer *mq.Producer
}

func NewBillingService(repo dao.BillingRepository, producer *mq.Producer) BillingService {
	return &billingServiceImpl{repo: repo, producer: producer}
}

func (s *billingServiceImpl) Deposit(ctx context.Context, userID string, amount float64, paymentMethod string, metadata map[string]string) (*model.Transaction, error) {
	logger.Info("开始充值",
		logger.StringField("userID", userID),
		logger.Float64Field("amount", amount))

	var resultTx *model.Transaction

	err := s.repo.WithTransaction(ctx, func(txRepo dao.BillingRepository) error {
		account, err := txRepo.GetAccountByUserID(ctx, userID)
		if err != nil {
			return err
		}

		if account == nil {
			account = &model.Account{
				UserID:  userID,
				Balance: 0,
				Usage:   map[string]string{},
			}
			if err = txRepo.CreateAccount(ctx, account); err != nil {
				return err
			}
		}

		balanceBefore := account.Balance
		account.Balance += amount
		if err = txRepo.UpdateAccount(ctx, account); err != nil {
			return err
		}

		tx := &model.Transaction{
			UserID:        userID,
			AccountID:     account.ID,
			Type:          1,
			Item:          0,
			Amount:        amount,
			BalanceBefore: balanceBefore,
			BalanceAfter:  account.Balance,
			Description:   "账户充值",
			Status:        2,
			Metadata:      model.JSONMap(metadata),
		}
		if err = txRepo.CreateTransaction(ctx, tx); err != nil {
			return err
		}

		resultTx = tx
		return nil
	})

	if err != nil {
		logger.Error("充值失败", logger.ErrorField(err))
		return nil, ErrInternalServer
	}

	s.publishBillingEvent(ctx, "deposit", userID, resultTx.ID, map[string]string{
		"amount": fmt.Sprintf("%.6f", amount),
	})

	logger.Info("充值成功",
		logger.StringField("userID", userID),
		logger.Float64Field("amount", amount))

	return resultTx, nil
}

// GetAccount 获取账户
func (s *billingServiceImpl) GetAccount(ctx context.Context, userID string) (*model.Account, error) {
	account, err := s.repo.GetAccountByUserID(ctx, userID)
	if err != nil {
		logger.Error("获取账户失败", logger.ErrorField(err))
		return nil, ErrInternalServer
	}

	if account == nil {
		account = &model.Account{
			UserID:  userID,
			Balance: 0,
			Usage:   map[string]string{},
		}
		if err = s.repo.CreateAccount(ctx, account); err != nil {
			return nil, err
		}
	}

	return account, nil
}

// GetTransactions 获取交易记录
func (s *billingServiceImpl) GetTransactions(ctx context.Context, userID string, txType *int, startTime, endTime *time.Time, page, pageSize int) ([]*model.Transaction, int64, error) {
	txs, total, err := s.repo.ListTransactions(ctx, userID, txType, startTime, endTime, page, pageSize)
	if err != nil {
		logger.Error("获取交易失败", logger.ErrorField(err))
		return nil, 0, ErrInternalServer
	}
	return txs, total, nil
}

func (s *billingServiceImpl) GetUsageStats(ctx context.Context, userID string, item *int, startTime, endTime *time.Time) (map[string]float64, error) {
	return s.repo.AggregateUsageByItem(ctx, userID, item, startTime, endTime)
}

func (s *billingServiceImpl) ConsumeModelCall(ctx context.Context, userID string, provider string, modelName string, tokenCount int, metadata map[string]string) (float64, error) {
	logger.Info("模型调用计费",
		logger.StringField("userID", userID),
		logger.StringField("provider", provider),
		logger.StringField("modelName", modelName),
		logger.IntField("tokenCount", tokenCount))

	amount := s.calculateModelCallPrice(provider, modelName, tokenCount)

	if metadata == nil {
		metadata = map[string]string{}
	}
	metadata["provider"] = provider
	metadata["model"] = modelName
	metadata["tokens"] = strconv.Itoa(tokenCount)
	description := "模型调用：" + provider + " " + modelName

	tx, err := s.repo.ConsumeBalance(ctx, userID, 1, amount, description, metadata)
	if err != nil {
		logger.Error("计费失败", logger.ErrorField(err))
		return 0, err
	}

	s.updateAccountUsage(ctx, userID, "model_call", amount)
	s.publishBillingEvent(ctx, "consume_model_call", userID, tx.ID, metadata)

	logger.Info("计费成功", logger.Float64Field("amount", amount))
	return amount, nil
}

func (s *billingServiceImpl) ConsumeEmbedding(ctx context.Context, userID string, provider string, modelName string, tokenCount int, vectorCount int, metadata map[string]string) (float64, error) {
	logger.Info("嵌入计费",
		logger.StringField("userID", userID),
		logger.StringField("provider", provider),
		logger.StringField("modelName", modelName),
		logger.IntField("tokenCount", tokenCount),
		logger.IntField("vectorCount", vectorCount))

	amount := s.calculateEmbeddingPrice(provider, modelName, tokenCount, vectorCount)

	if metadata == nil {
		metadata = map[string]string{}
	}
	metadata["provider"] = provider
	metadata["model"] = modelName
	metadata["tokens"] = strconv.Itoa(tokenCount)
	metadata["vector_count"] = strconv.Itoa(vectorCount)
	description := "向量嵌入：" + provider + " " + modelName

	tx, err := s.repo.ConsumeBalance(ctx, userID, 4, amount, description, metadata)
	if err != nil {
		logger.Error("嵌入计费失败", logger.ErrorField(err))
		return 0, err
	}

	s.updateAccountUsage(ctx, userID, "embedding", amount)
	s.publishBillingEvent(ctx, "consume_embedding", userID, tx.ID, metadata)

	logger.Info("嵌入计费成功", logger.Float64Field("amount", amount))
	return amount, nil
}

func (s *billingServiceImpl) Withdraw(ctx context.Context, userID string, amount float64, withdrawMethod string, metadata map[string]string) (*model.Transaction, error) {
	logger.Info("开始提现",
		logger.StringField("userID", userID),
		logger.Float64Field("amount", amount))

	if amount <= 0 {
		return nil, errors.New("提现金额必须大于0")
	}

	var resultTx *model.Transaction

	err := s.repo.WithTransaction(ctx, func(txRepo dao.BillingRepository) error {
		account, err := txRepo.GetAccountByUserID(ctx, userID)
		if err != nil {
			return err
		}
		if account == nil {
			return ErrAccountNotFound
		}

		if account.Balance < amount {
			return ErrInsufficientFund
		}

		balanceBefore := account.Balance
		account.Balance -= amount
		if err = txRepo.UpdateAccount(ctx, account); err != nil {
			return err
		}

		tx := &model.Transaction{
			UserID:        userID,
			AccountID:     account.ID,
			Type:          2,
			Item:          0,
			Amount:        amount,
			BalanceBefore: balanceBefore,
			BalanceAfter:  account.Balance,
			Description:   "账户提现",
			Status:        2,
			Metadata:      model.JSONMap(metadata),
		}
		if err = txRepo.CreateTransaction(ctx, tx); err != nil {
			return err
		}

		resultTx = tx
		return nil
	})

	if err != nil {
		logger.Error("提现失败", logger.ErrorField(err))
		return nil, err
	}

	s.publishBillingEvent(ctx, "withdraw", userID, resultTx.ID, map[string]string{
		"amount": fmt.Sprintf("%.6f", amount),
		"method": withdrawMethod,
	})

	logger.Info("提现成功",
		logger.StringField("userID", userID),
		logger.Float64Field("amount", amount))

	return resultTx, nil
}

func (s *billingServiceImpl) Refund(ctx context.Context, userID string, transactionID string, amount float64, reason string, metadata map[string]string) (*model.Transaction, error) {
	logger.Info("开始退款",
		logger.StringField("userID", userID),
		logger.StringField("transactionID", transactionID),
		logger.Float64Field("amount", amount))

	if amount <= 0 {
		return nil, errors.New("退款金额必须大于0")
	}

	var resultTx *model.Transaction

	err := s.repo.WithTransaction(ctx, func(txRepo dao.BillingRepository) error {
		account, err := txRepo.GetAccountByUserID(ctx, userID)
		if err != nil {
			return err
		}
		if account == nil {
			return ErrAccountNotFound
		}

		balanceBefore := account.Balance
		account.Balance += amount
		if err = txRepo.UpdateAccount(ctx, account); err != nil {
			return err
		}

		tx := &model.Transaction{
			UserID:        userID,
			AccountID:     account.ID,
			Type:          4,
			Item:          0,
			Amount:        amount,
			BalanceBefore: balanceBefore,
			BalanceAfter:  account.Balance,
			Description:   "退款：" + reason,
			Status:        2,
			Metadata: model.JSONMap(map[string]string{
				"original_transaction_id": transactionID,
				"reason":                  reason,
			}),
		}
		if metadata != nil {
			for k, v := range metadata {
				tx.Metadata[k] = v
			}
		}
		if err = txRepo.CreateTransaction(ctx, tx); err != nil {
			return err
		}

		resultTx = tx
		return nil
	})

	if err != nil {
		logger.Error("退款失败", logger.ErrorField(err))
		return nil, err
	}

	s.publishBillingEvent(ctx, "refund", userID, resultTx.ID, map[string]string{
		"original_transaction_id": transactionID,
		"amount":                  fmt.Sprintf("%.6f", amount),
		"reason":                  reason,
	})

	logger.Info("退款成功",
		logger.StringField("userID", userID),
		logger.Float64Field("amount", amount))

	return resultTx, nil
}

func (s *billingServiceImpl) calculateModelCallPrice(provider string, modelName string, tokenCount int) float64 {
	pricePerK := 0.01

	switch provider {
	case "openai":
		switch modelName {
		case "gpt-4o":
			pricePerK = 0.005
		case "gpt-4o-mini":
			pricePerK = 0.00015
		case "gpt-4-turbo":
			pricePerK = 0.01
		case "gpt-3.5-turbo":
			pricePerK = 0.0005
		default:
			pricePerK = 0.002
		}
	case "anthropic", "claude":
		switch modelName {
		case "claude-3-opus":
			pricePerK = 0.015
		case "claude-3-sonnet":
			pricePerK = 0.003
		case "claude-3-haiku":
			pricePerK = 0.00025
		default:
			pricePerK = 0.003
		}
	case "deepseek":
		switch modelName {
		case "deepseek-chat":
			pricePerK = 0.00014
		case "deepseek-coder":
			pricePerK = 0.00014
		default:
			pricePerK = 0.0002
		}
	case "chatglm", "qianfan":
		pricePerK = 0.001
	case "platform":
		pricePerK = 0.005
	}

	return float64(tokenCount) / 1000 * pricePerK
}

func (s *billingServiceImpl) calculateEmbeddingPrice(provider string, modelName string, tokenCount int, vectorCount int) float64 {
	pricePerK := 0.0001

	switch provider {
	case "openai":
		switch modelName {
		case "text-embedding-3-small":
			pricePerK = 0.00002
		case "text-embedding-3-large":
			pricePerK = 0.00013
		case "text-embedding-ada-002":
			pricePerK = 0.0001
		default:
			pricePerK = 0.0001
		}
	case "deepseek":
		pricePerK = 0.00002
	case "chatglm", "qianfan":
		pricePerK = 0.00005
	case "platform":
		pricePerK = 0.0001
	}

	tokenCost := float64(tokenCount) / 1000 * pricePerK
	vectorCost := float64(vectorCount) * 0.00001
	return tokenCost + vectorCost
}

func (s *billingServiceImpl) ConsumeStorage(ctx context.Context, userID string, storageType string, sizeBytes int64, metadata map[string]string) (float64, error) {
	logger.Info("存储计费",
		logger.StringField("userID", userID),
		logger.StringField("storageType", storageType),
		logger.Int64Field("sizeBytes", sizeBytes))

	amount := s.calculateStoragePrice(storageType, sizeBytes)

	if metadata == nil {
		metadata = map[string]string{}
	}
	metadata["storage_type"] = storageType
	metadata["size_bytes"] = strconv.FormatInt(sizeBytes, 10)
	description := "存储使用：" + storageType

	tx, err := s.repo.ConsumeBalance(ctx, userID, 2, amount, description, metadata)
	if err != nil {
		logger.Error("存储计费失败", logger.ErrorField(err))
		return 0, err
	}

	s.updateAccountUsage(ctx, userID, "storage", amount)
	s.publishBillingEvent(ctx, "consume_storage", userID, tx.ID, metadata)

	logger.Info("存储计费成功", logger.Float64Field("amount", amount))
	return amount, nil
}

func (s *billingServiceImpl) ConsumeBandwidth(ctx context.Context, userID string, bandwidthType string, bytes int64, metadata map[string]string) (float64, error) {
	logger.Info("带宽计费",
		logger.StringField("userID", userID),
		logger.StringField("bandwidthType", bandwidthType),
		logger.Int64Field("bytes", bytes))

	amount := s.calculateBandwidthPrice(bandwidthType, bytes)

	if metadata == nil {
		metadata = map[string]string{}
	}
	metadata["bandwidth_type"] = bandwidthType
	metadata["bytes"] = strconv.FormatInt(bytes, 10)
	description := "带宽使用：" + bandwidthType

	tx, err := s.repo.ConsumeBalance(ctx, userID, 3, amount, description, metadata)
	if err != nil {
		logger.Error("带宽计费失败", logger.ErrorField(err))
		return 0, err
	}

	s.updateAccountUsage(ctx, userID, "bandwidth", amount)
	s.publishBillingEvent(ctx, "consume_bandwidth", userID, tx.ID, metadata)

	logger.Info("带宽计费成功", logger.Float64Field("amount", amount))
	return amount, nil
}

func (s *billingServiceImpl) calculateStoragePrice(storageType string, sizeBytes int64) float64 {
	pricePerGB := 0.001
	switch storageType {
	case "ssd":
		pricePerGB = 0.002
	case "hdd":
		pricePerGB = 0.001
	case "vector":
		pricePerGB = 0.005
	}
	gb := float64(sizeBytes) / (1024 * 1024 * 1024)
	return gb * pricePerGB
}

func (s *billingServiceImpl) calculateBandwidthPrice(bandwidthType string, bytes int64) float64 {
	pricePerGB := 0.0005
	switch bandwidthType {
	case "cdn":
		pricePerGB = 0.001
	case "api":
		pricePerGB = 0.0005
	case "internal":
		pricePerGB = 0.0001
	}
	gb := float64(bytes) / (1024 * 1024 * 1024)
	return gb * pricePerGB
}

func (s *billingServiceImpl) updateAccountUsage(ctx context.Context, userID string, itemKey string, amount float64) {
	account, err := s.repo.GetAccountByUserID(ctx, userID)
	if err != nil || account == nil {
		return
	}

	if account.Usage == nil {
		account.Usage = map[string]string{}
	}

	currentStr, ok := account.Usage[itemKey]
	current := 0.0
	if ok {
		if v, err := strconv.ParseFloat(currentStr, 64); err == nil {
			current = v
		}
	}
	current += amount
	account.Usage[itemKey] = fmt.Sprintf("%.6f", current)

	if err := s.repo.UpdateAccount(ctx, account); err != nil {
		logger.Warn("更新账户Usage失败", logger.ErrorField(err))
	}
}

func (s *billingServiceImpl) publishBillingEvent(ctx context.Context, action, userID, txID string, metadata map[string]string) {
	if s.producer == nil {
		return
	}

	event := map[string]interface{}{
		"action":         action,
		"user_id":        userID,
		"transaction_id": txID,
		"metadata":       metadata,
		"timestamp":      time.Now().Unix(),
	}

	eventJSON, err := json.Marshal(event)
	if err != nil {
		logger.Warn("序列化计费事件失败", logger.ErrorField(err))
		return
	}

	if err := s.producer.Send(ctx, "billing_events", userID, eventJSON); err != nil {
		logger.Warn("发送计费事件失败",
			logger.StringField("action", action),
			logger.StringField("userID", userID),
			logger.ErrorField(err))
	}
}

func (s *billingServiceImpl) WithTransaction(ctx context.Context, fn func(txService BillingService) error) error {
	return s.repo.WithTransaction(ctx, func(txRepo dao.BillingRepository) error {
		txService := &billingServiceImpl{repo: txRepo, producer: s.producer}
		return fn(txService)
	})
}
