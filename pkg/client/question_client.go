package client

import (
	"context"
	"encoding/json"
	"fmt"

	"Logos/config"
	pb "Logos/proto_gen/question"

	"google.golang.org/grpc"
)

type QuestionClient struct {
	client pb.QAServiceClient
	conn   *grpc.ClientConn
}

func NewQuestionClient(client pb.QAServiceClient, conn *grpc.ClientConn) *QuestionClient {
	return &QuestionClient{client: client, conn: conn}
}

func NewQuestionClientFromConfig(cfg *config.Config) (*QuestionClient, error) {
	conn, err := newConn(cfg, "logos.question")
	if err != nil {
		return nil, err
	}
	client := pb.NewQAServiceClient(conn)
	return NewQuestionClient(client, conn), nil
}

func (c *QuestionClient) AskQuestion(ctx context.Context, content string, userID int64) (string, error) {
	resp, err := c.client.AskQuestion(ctx, &pb.QuestionReq{
		Content: content,
		UserId:  userID,
	})
	if err != nil {
		return "", err
	}
	return resp.GetAnswer(), nil
}

func (c *QuestionClient) GetHistory(ctx context.Context, userID int64, page, pageSize int32) (*pb.HistoryResp, error) {
	return c.client.GetHistory(ctx, &pb.HistoryReq{
		UserId:   userID,
		Page:     page,
		PageSize: pageSize,
	})
}

func (c *QuestionClient) SubmitFeedback(ctx context.Context, questionID string, feedback string, rating *int32) error {
	_, err := c.client.SubmitFeedback(ctx, &pb.FeedbackReq{
		QuestionId: questionID,
		Feedback:   feedback,
		Rating:     rating,
	})
	return err
}

func (c *QuestionClient) GetRecommendedQuestions(ctx context.Context, userID int64) ([]string, error) {
	resp, err := c.client.GetRecommendedQuestions(ctx, &pb.GetRecommendedQuestionsReq{
		UserId: userID,
	})
	if err != nil {
		return nil, err
	}
	if resp == nil || resp.StatusMessage == "" {
		return nil, nil
	}

	// 下游把推荐结果放在 BaseResp.StatusMessage 的 JSON 里，形状是
	// {"questions": [...]} 而不是裸数组。旧实现按 []string 解析并把错误
	// 丢掉，结果恒为空切片——调用方无法区分「模型没生成」和「契约变了」。
	var payload struct {
		Questions []string `json:"questions"`
	}
	if err := json.Unmarshal([]byte(resp.StatusMessage), &payload); err != nil {
		return nil, fmt.Errorf("解析推荐问题响应失败: %w", err)
	}

	return payload.Questions, nil
}

func (c *QuestionClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}
