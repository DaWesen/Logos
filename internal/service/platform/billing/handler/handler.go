package handler

import (
	"context"
	"strconv"
	"time"

	"Logos/internal/service/platform/billing/model"
	"Logos/internal/service/platform/billing/service"
	"Logos/pkg/logger"
	pb "Logos/proto_gen/billing"

	"google.golang.org/protobuf/types/known/timestamppb"
)

type BillingServiceImpl struct {
	pb.UnimplementedBillingServiceServer
	BillingService service.BillingService
}

func convertModelAccountToProtoAccount(ma *model.Account) *pb.Account {
	if ma == nil {
		return nil
	}

	usage := make(map[string]float64)
	if ma.Usage != nil {
		for k, v := range ma.Usage {
			if val, err := strconv.ParseFloat(v, 64); err == nil {
				usage[k] = val
			}
		}
	}

	return &pb.Account{
		Id:          ma.ID,
		UserId:      ma.UserID,
		Balance:     ma.Balance,
		CreditLimit: ma.CreditLimit,
		Usage:       usage,
		CreatedAt:   timestamppb.New(ma.CreatedAt),
		UpdatedAt:   timestamppb.New(ma.UpdatedAt),
	}
}

func convertModelTransactionToProtoTransaction(mt *model.Transaction) *pb.Transaction {
	if mt == nil {
		return nil
	}

	var txType pb.TransactionType
	switch mt.Type {
	case 1:
		txType = pb.TransactionType_TRANSACTION_TYPE_DEPOSIT
	case 2:
		txType = pb.TransactionType_TRANSACTION_TYPE_WITHDRAWAL
	case 3:
		txType = pb.TransactionType_TRANSACTION_TYPE_CONSUME
	case 4:
		txType = pb.TransactionType_TRANSACTION_TYPE_REFUND
	default:
		txType = pb.TransactionType_TRANSACTION_TYPE_UNSPECIFIED
	}

	var txStatus pb.TransactionStatus
	switch mt.Status {
	case 1:
		txStatus = pb.TransactionStatus_TRANSACTION_STATUS_PENDING
	case 2:
		txStatus = pb.TransactionStatus_TRANSACTION_STATUS_SUCCESS
	case 3:
		txStatus = pb.TransactionStatus_TRANSACTION_STATUS_FAILED
	default:
		txStatus = pb.TransactionStatus_TRANSACTION_STATUS_UNSPECIFIED
	}

	var item pb.BillingItem
	switch mt.Item {
	case 1:
		item = pb.BillingItem_BILLING_ITEM_MODEL_CALL
	case 2:
		item = pb.BillingItem_BILLING_ITEM_STORAGE
	case 3:
		item = pb.BillingItem_BILLING_ITEM_BANDWIDTH
	case 4:
		item = pb.BillingItem_BILLING_ITEM_EMBEDDING
	default:
		item = pb.BillingItem_BILLING_ITEM_UNSPECIFIED
	}

	return &pb.Transaction{
		Id:            mt.ID,
		UserId:        mt.UserID,
		AccountId:     mt.AccountID,
		Type:          txType,
		Item:          item,
		Amount:        mt.Amount,
		BalanceBefore: mt.BalanceBefore,
		BalanceAfter:  mt.BalanceAfter,
		Description:   mt.Description,
		Status:        txStatus,
		Metadata:      map[string]string(mt.Metadata),
		CreatedAt:     timestamppb.New(mt.CreatedAt),
	}
}

func (s *BillingServiceImpl) Deposit(ctx context.Context, req *pb.DepositRequest) (*pb.DepositResponse, error) {
	resp := &pb.DepositResponse{}

	userID := req.UserId
	tx, err := s.BillingService.Deposit(ctx, userID, req.Amount, req.PaymentMethod, req.Metadata)
	if err != nil {
		logger.Error("充值失败", logger.ErrorField(err))
		resp.Code = 1
		resp.Message = err.Error()
		return resp, nil
	}

	resp.Code = 0
	resp.Message = "success"
	resp.Data = convertModelTransactionToProtoTransaction(tx)
	return resp, nil
}

func (s *BillingServiceImpl) ConsumeStorage(ctx context.Context, req *pb.ConsumeStorageRequest) (*pb.ConsumeStorageResponse, error) {
	resp := &pb.ConsumeStorageResponse{}

	userID := req.UserId
	amount, err := s.BillingService.ConsumeStorage(ctx, userID, req.StorageType, req.SizeBytes, req.Metadata)
	if err != nil {
		logger.Error("存储计费失败", logger.ErrorField(err))
		resp.Code = 1
		resp.Message = err.Error()
		return resp, nil
	}

	resp.Code = 0
	resp.Message = "success"
	resp.Amount = amount
	return resp, nil
}

func (s *BillingServiceImpl) ConsumeBandwidth(ctx context.Context, req *pb.ConsumeBandwidthRequest) (*pb.ConsumeBandwidthResponse, error) {
	resp := &pb.ConsumeBandwidthResponse{}

	userID := req.UserId
	amount, err := s.BillingService.ConsumeBandwidth(ctx, userID, req.BandwidthType, req.Bytes, req.Metadata)
	if err != nil {
		logger.Error("带宽计费失败", logger.ErrorField(err))
		resp.Code = 1
		resp.Message = err.Error()
		return resp, nil
	}

	resp.Code = 0
	resp.Message = "success"
	resp.Amount = amount
	return resp, nil
}

func (s *BillingServiceImpl) GetAccount(ctx context.Context, req *pb.GetAccountRequest) (*pb.GetAccountResponse, error) {
	resp := &pb.GetAccountResponse{}

	userID := req.UserId
	account, err := s.BillingService.GetAccount(ctx, userID)
	if err != nil {
		logger.Error("获取账户失败", logger.ErrorField(err))
		resp.Code = 1
		resp.Message = err.Error()
		return resp, nil
	}

	resp.Code = 0
	resp.Message = "success"
	resp.Data = convertModelAccountToProtoAccount(account)
	return resp, nil
}

func (s *BillingServiceImpl) GetTransactions(ctx context.Context, req *pb.GetTransactionsRequest) (*pb.GetTransactionsResponse, error) {
	resp := &pb.GetTransactionsResponse{}

	userID := req.UserId
	var txType *int
	if req.Type != pb.TransactionType_TRANSACTION_TYPE_UNSPECIFIED {
		t := int(req.Type)
		txType = &t
	}

	var startTime, endTime *time.Time
	if req.StartTime != nil {
		t := req.StartTime.AsTime()
		startTime = &t
	}
	if req.EndTime != nil {
		t := req.EndTime.AsTime()
		endTime = &t
	}

	txs, total, err := s.BillingService.GetTransactions(ctx, userID, txType, startTime, endTime, int(req.Page), int(req.PageSize))
	if err != nil {
		logger.Error("获取交易记录失败", logger.ErrorField(err))
		resp.Code = 1
		resp.Message = err.Error()
		return resp, nil
	}

	resp.Code = 0
	resp.Message = "success"
	resp.Total = int32(total)
	for _, tx := range txs {
		resp.Transactions = append(resp.Transactions, convertModelTransactionToProtoTransaction(tx))
	}
	return resp, nil
}

func (s *BillingServiceImpl) GetUsageStats(ctx context.Context, req *pb.GetUsageStatsRequest) (*pb.GetUsageStatsResponse, error) {
	resp := &pb.GetUsageStatsResponse{}

	userID := req.UserId
	var item *int
	if req.Item != pb.BillingItem_BILLING_ITEM_UNSPECIFIED {
		i := int(req.Item)
		item = &i
	}

	var startTime, endTime *time.Time
	if req.StartTime != nil {
		t := req.StartTime.AsTime()
		startTime = &t
	}
	if req.EndTime != nil {
		t := req.EndTime.AsTime()
		endTime = &t
	}

	stats, err := s.BillingService.GetUsageStats(ctx, userID, item, startTime, endTime)
	if err != nil {
		logger.Error("获取用量统计失败", logger.ErrorField(err))
		resp.Code = 1
		resp.Message = err.Error()
		return resp, nil
	}

	resp.Code = 0
	resp.Message = "success"
	resp.Stats = stats
	return resp, nil
}

func (s *BillingServiceImpl) ConsumeModelCall(ctx context.Context, req *pb.ConsumeModelCallRequest) (*pb.ConsumeModelCallResponse, error) {
	resp := &pb.ConsumeModelCallResponse{}

	userID := req.UserId
	amount, err := s.BillingService.ConsumeModelCall(ctx, userID, req.Provider, req.ModelName, int(req.TokenCount), req.Metadata)
	if err != nil {
		logger.Error("模型调用计费失败", logger.ErrorField(err))
		resp.Code = 1
		resp.Message = err.Error()
		return resp, nil
	}

	resp.Code = 0
	resp.Message = "success"
	resp.Amount = amount
	return resp, nil
}

func (s *BillingServiceImpl) Withdraw(ctx context.Context, req *pb.WithdrawRequest) (*pb.WithdrawResponse, error) {
	resp := &pb.WithdrawResponse{}

	userID := req.UserId
	tx, err := s.BillingService.Withdraw(ctx, userID, req.Amount, req.WithdrawMethod, req.Metadata)
	if err != nil {
		logger.Error("提现失败", logger.ErrorField(err))
		resp.Code = 1
		resp.Message = err.Error()
		return resp, nil
	}

	resp.Code = 0
	resp.Message = "success"
	resp.Data = convertModelTransactionToProtoTransaction(tx)
	return resp, nil
}

func (s *BillingServiceImpl) ConsumeEmbedding(ctx context.Context, req *pb.ConsumeEmbeddingRequest) (*pb.ConsumeEmbeddingResponse, error) {
	resp := &pb.ConsumeEmbeddingResponse{}

	userID := req.UserId
	amount, err := s.BillingService.ConsumeEmbedding(ctx, userID, req.Provider, req.ModelName, int(req.TokenCount), int(req.VectorCount), req.Metadata)
	if err != nil {
		logger.Error("嵌入计费失败", logger.ErrorField(err))
		resp.Code = 1
		resp.Message = err.Error()
		return resp, nil
	}

	resp.Code = 0
	resp.Message = "success"
	resp.Amount = amount
	return resp, nil
}

func (s *BillingServiceImpl) Refund(ctx context.Context, req *pb.RefundRequest) (*pb.RefundResponse, error) {
	resp := &pb.RefundResponse{}

	userID := req.UserId
	tx, err := s.BillingService.Refund(ctx, userID, req.TransactionId, req.Amount, req.Reason, req.Metadata)
	if err != nil {
		logger.Error("退款失败", logger.ErrorField(err))
		resp.Code = 1
		resp.Message = err.Error()
		return resp, nil
	}

	resp.Code = 0
	resp.Message = "success"
	resp.Data = convertModelTransactionToProtoTransaction(tx)
	return resp, nil
}
