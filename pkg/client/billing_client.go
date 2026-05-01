package client

import (
	"context"
	"fmt"

	pb "Logos/proto_gen/billing"
	"Logos/pkg/logger"

	"google.golang.org/grpc"
)

type BillingClient struct {
	client pb.BillingServiceClient
	conn   *grpc.ClientConn
}

func NewBillingClient(client pb.BillingServiceClient, conn *grpc.ClientConn) *BillingClient {
	return &BillingClient{client: client, conn: conn}
}

func (c *BillingClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

func (c *BillingClient) ConsumeModelCall(ctx context.Context, userID string, provider string, modelName string, tokenCount int, metadata map[string]string) (float64, error) {
	if c.client == nil {
		logger.Warn("BillingClient未初始化，跳过计费")
		return 0, nil
	}

	resp, err := c.client.ConsumeModelCall(ctx, &pb.ConsumeModelCallRequest{
		UserId:     userID,
		Provider:   provider,
		ModelName:  modelName,
		TokenCount: int32(tokenCount),
		Metadata:   metadata,
	})
	if err != nil {
		logger.Error("RPC调用ConsumeModelCall失败", logger.ErrorField(err))
		return 0, err
	}
	if resp.Code != 0 {
		return 0, fmt.Errorf("%s", resp.Message)
	}

	return resp.Amount, nil
}

func (c *BillingClient) ConsumeStorage(ctx context.Context, userID string, storageType string, sizeBytes int64) (float64, error) {
	if c.client == nil {
		logger.Warn("BillingClient未初始化，跳过存储计费")
		return 0, nil
	}

	resp, err := c.client.ConsumeStorage(ctx, &pb.ConsumeStorageRequest{
		UserId:      userID,
		StorageType: storageType,
		SizeBytes:   sizeBytes,
	})
	if err != nil {
		logger.Error("RPC调用ConsumeStorage失败", logger.ErrorField(err))
		return 0, err
	}
	if resp.Code != 0 {
		return 0, fmt.Errorf("%s", resp.Message)
	}

	return resp.Amount, nil
}

func (c *BillingClient) ConsumeBandwidth(ctx context.Context, userID string, bandwidthType string, bytes int64) (float64, error) {
	if c.client == nil {
		logger.Warn("BillingClient未初始化，跳过带宽计费")
		return 0, nil
	}

	resp, err := c.client.ConsumeBandwidth(ctx, &pb.ConsumeBandwidthRequest{
		UserId:        userID,
		BandwidthType: bandwidthType,
		Bytes:         bytes,
	})
	if err != nil {
		logger.Error("RPC调用ConsumeBandwidth失败", logger.ErrorField(err))
		return 0, err
	}
	if resp.Code != 0 {
		return 0, fmt.Errorf("%s", resp.Message)
	}

	return resp.Amount, nil
}

func (c *BillingClient) Deposit(ctx context.Context, userID string, amount float64, paymentMethod string, metadata map[string]string) (string, error) {
	if c.client == nil {
		logger.Warn("BillingClient未初始化，跳过充值")
		return "", nil
	}

	resp, err := c.client.Deposit(ctx, &pb.DepositRequest{
		UserId:        userID,
		Amount:        amount,
		PaymentMethod: paymentMethod,
		Metadata:      metadata,
	})
	if err != nil {
		logger.Error("RPC调用Deposit失败", logger.ErrorField(err))
		return "", err
	}
	if resp.Code != 0 {
		return "", fmt.Errorf("%s", resp.Message)
	}

	txID := ""
	if resp.Data != nil {
		txID = resp.Data.Id
	}
	return txID, nil
}

func (c *BillingClient) GetAccount(ctx context.Context, userID string) (float64, error) {
	if c.client == nil {
		logger.Warn("BillingClient未初始化，返回0余额")
		return 0, nil
	}

	resp, err := c.client.GetAccount(ctx, &pb.GetAccountRequest{
		UserId: userID,
	})
	if err != nil {
		logger.Error("RPC调用GetAccount失败", logger.ErrorField(err))
		return 0, err
	}
	if resp.Code != 0 {
		return 0, fmt.Errorf("%s", resp.Message)
	}

	balance := 0.0
	if resp.Data != nil {
		balance = resp.Data.Balance
	}
	return balance, nil
}
