package service

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strconv"
	"strings"
	"time"

	"Logos/internal/service/platform/billing/dao"
	"Logos/internal/service/platform/billing/model"
	"Logos/pkg/logger"
	"Logos/pkg/outbox"

	"gorm.io/gorm"
)

var (
	ErrAccountNotFound  = errors.New("account not found")
	ErrInsufficientFund = errors.New("insufficient fund")
	ErrInternalServer   = errors.New("internal server error")
)

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
	repo       dao.BillingRepository
	outboxRepo outbox.OutboxRepository
}

func NewBillingService(repo dao.BillingRepository, outboxRepo outbox.OutboxRepository) BillingService {
	return &billingServiceImpl{repo: repo, outboxRepo: outboxRepo}
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

		return s.saveBillingEventToOutbox(ctx, txRepo.DB(), "deposit", userID, tx.ID, map[string]string{
			"amount": fmt.Sprintf("%.6f", amount),
		})
	})

	if err != nil {
		logger.Error("充值失败", logger.ErrorField(err))
		return nil, ErrInternalServer
	}

	logger.Info("充值成功",
		logger.StringField("userID", userID),
		logger.Float64Field("amount", amount))

	return resultTx, nil
}

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

	// 从 metadata 中读取 input_tokens 和 output_tokens
	inputTokens := tokenCount
	outputTokens := 0
	cacheHitTokens := 0
	if metadata != nil {
		if v, ok := metadata["input_tokens"]; ok {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				inputTokens = n
			}
		}
		if v, ok := metadata["output_tokens"]; ok {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				outputTokens = n
			}
		}
		if v, ok := metadata["cache_hit_tokens"]; ok {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				cacheHitTokens = n
			}
		}
	}

	inputPrice, outputPrice := s.getModelTokenPrices(provider, modelName)
	cacheHitPrice := s.getCacheHitInputPrice(inputPrice)
	// 缓存命中的 tokens 从输入 tokens 中扣除，按缓存命中价格计费
	cacheMissTokens := inputTokens - cacheHitTokens
	if cacheMissTokens < 0 {
		cacheMissTokens = 0
	}
	amount := float64(cacheMissTokens)/1_000_000*inputPrice + float64(cacheHitTokens)/1_000_000*cacheHitPrice + float64(outputTokens)/1_000_000*outputPrice

	if metadata == nil {
		metadata = map[string]string{}
	}
	metadata["provider"] = provider
	metadata["model"] = modelName
	metadata["tokens"] = strconv.Itoa(tokenCount)
	metadata["input_tokens"] = strconv.Itoa(inputTokens)
	metadata["output_tokens"] = strconv.Itoa(outputTokens)
	metadata["cache_hit_tokens"] = strconv.Itoa(cacheHitTokens)
	metadata["input_price"] = fmt.Sprintf("%.3f", inputPrice)
	metadata["output_price"] = fmt.Sprintf("%.3f", outputPrice)
	metadata["cache_hit_price"] = fmt.Sprintf("%.3f", cacheHitPrice)
	description := "模型调用：" + provider + " " + modelName

	err := s.repo.WithTransaction(ctx, func(txRepo dao.BillingRepository) error {
		tx, err := txRepo.ConsumeBalance(ctx, userID, 1, amount, description, metadata)
		if err != nil {
			return err
		}

		s.updateAccountUsageInTx(ctx, txRepo, userID, "model_call", amount)

		return s.saveBillingEventToOutbox(ctx, txRepo.DB(), "consume_model_call", userID, tx.ID, metadata)
	})
	if err != nil {
		logger.Error("计费失败", logger.ErrorField(err))
		return 0, err
	}

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

	err := s.repo.WithTransaction(ctx, func(txRepo dao.BillingRepository) error {
		tx, err := txRepo.ConsumeBalance(ctx, userID, 4, amount, description, metadata)
		if err != nil {
			return err
		}

		s.updateAccountUsageInTx(ctx, txRepo, userID, "embedding", amount)

		return s.saveBillingEventToOutbox(ctx, txRepo.DB(), "consume_embedding", userID, tx.ID, metadata)
	})
	if err != nil {
		logger.Error("嵌入计费失败", logger.ErrorField(err))
		return 0, err
	}

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

		return s.saveBillingEventToOutbox(ctx, txRepo.DB(), "withdraw", userID, tx.ID, map[string]string{
			"amount": fmt.Sprintf("%.6f", amount),
			"method": withdrawMethod,
		})
	})

	if err != nil {
		logger.Error("提现失败", logger.ErrorField(err))
		return nil, err
	}

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
		maps.Copy(tx.Metadata, metadata)
		if err = txRepo.CreateTransaction(ctx, tx); err != nil {
			return err
		}

		resultTx = tx

		return s.saveBillingEventToOutbox(ctx, txRepo.DB(), "refund", userID, tx.ID, map[string]string{
			"original_transaction_id": transactionID,
			"amount":                  fmt.Sprintf("%.6f", amount),
			"reason":                  reason,
		})
	})

	if err != nil {
		logger.Error("退款失败", logger.ErrorField(err))
		return nil, err
	}

	logger.Info("退款成功",
		logger.StringField("userID", userID),
		logger.Float64Field("amount", amount))

	return resultTx, nil
}

// getModelTokenPrices 返回 (输入价格/百万tokens[缓存未命中], 输出价格/百万tokens)
// 定价标准：
//   - 标准：输入(缓存未命中) 1元/百万tokens，输出 2元/百万tokens
//   - 高级(gpt-4o等)：输入(缓存未命中) 3元/百万tokens，输出 6元/百万tokens
//   - 缓存命中输入：0.02元/百万tokens(标准) / 0.025元/百万tokens(高级)
func (s *billingServiceImpl) getModelTokenPrices(provider string, modelName string) (float64, float64) {
	// 默认标准价格
	inputPrice := 1.0
	outputPrice := 2.0

	switch provider {
	case "openai":
		switch {
		case strings.Contains(modelName, "gpt-4o") && !strings.Contains(modelName, "mini"):
			inputPrice = 3.0
			outputPrice = 6.0
		case strings.Contains(modelName, "gpt-4o-mini"):
			inputPrice = 1.0
			outputPrice = 2.0
		case strings.Contains(modelName, "gpt-4-turbo"):
			inputPrice = 3.0
			outputPrice = 6.0
		case strings.Contains(modelName, "gpt-3.5-turbo"):
			inputPrice = 1.0
			outputPrice = 2.0
		default:
			inputPrice = 3.0
			outputPrice = 6.0
		}
	case "anthropic", "claude":
		switch {
		case strings.Contains(modelName, "opus"):
			inputPrice = 3.0
			outputPrice = 6.0
		case strings.Contains(modelName, "sonnet"):
			inputPrice = 3.0
			outputPrice = 6.0
		case strings.Contains(modelName, "haiku"):
			inputPrice = 1.0
			outputPrice = 2.0
		default:
			inputPrice = 3.0
			outputPrice = 6.0
		}
	case "deepseek":
		inputPrice = 1.0
		outputPrice = 2.0
	case "doubao":
		inputPrice = 1.0
		outputPrice = 2.0
	case "chatglm", "qianfan":
		inputPrice = 1.0
		outputPrice = 2.0
	case "platform":
		inputPrice = 1.0
		outputPrice = 2.0
	}

	return inputPrice, outputPrice
}

// getCacheHitInputPrice 返回缓存命中时的输入价格/百万tokens
func (s *billingServiceImpl) getCacheHitInputPrice(inputPrice float64) float64 {
	if inputPrice > 1.0 {
		return 0.025 // 高级模型缓存命中价格
	}
	return 0.02 // 标准模型缓存命中价格
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

	err := s.repo.WithTransaction(ctx, func(txRepo dao.BillingRepository) error {
		tx, err := txRepo.ConsumeBalance(ctx, userID, 2, amount, description, metadata)
		if err != nil {
			return err
		}

		s.updateAccountUsageInTx(ctx, txRepo, userID, "storage", amount)

		return s.saveBillingEventToOutbox(ctx, txRepo.DB(), "consume_storage", userID, tx.ID, metadata)
	})
	if err != nil {
		logger.Error("存储计费失败", logger.ErrorField(err))
		return 0, err
	}

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

	err := s.repo.WithTransaction(ctx, func(txRepo dao.BillingRepository) error {
		tx, err := txRepo.ConsumeBalance(ctx, userID, 3, amount, description, metadata)
		if err != nil {
			return err
		}

		s.updateAccountUsageInTx(ctx, txRepo, userID, "bandwidth", amount)

		return s.saveBillingEventToOutbox(ctx, txRepo.DB(), "consume_bandwidth", userID, tx.ID, metadata)
	})
	if err != nil {
		logger.Error("带宽计费失败", logger.ErrorField(err))
		return 0, err
	}

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

func (s *billingServiceImpl) updateAccountUsageInTx(ctx context.Context, txRepo dao.BillingRepository, userID string, itemKey string, amount float64) {
	account, err := txRepo.GetAccountByUserID(ctx, userID)
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

	if err := txRepo.UpdateAccount(ctx, account); err != nil {
		logger.Warn("更新账户Usage失败", logger.ErrorField(err))
	}
}

func (s *billingServiceImpl) saveBillingEventToOutbox(ctx context.Context, txDB *gorm.DB, action, userID, txID string, metadata map[string]string) error {
	event := map[string]any{
		"action":         action,
		"user_id":        userID,
		"transaction_id": txID,
		"metadata":       metadata,
		"timestamp":      time.Now().Unix(),
	}

	return s.outboxRepo.SaveWithTx(ctx, txDB, "billing_events", userID, event)
}

func (s *billingServiceImpl) WithTransaction(ctx context.Context, fn func(txService BillingService) error) error {
	return s.repo.WithTransaction(ctx, func(txRepo dao.BillingRepository) error {
		txService := &billingServiceImpl{repo: txRepo, outboxRepo: s.outboxRepo}
		return fn(txService)
	})
}
