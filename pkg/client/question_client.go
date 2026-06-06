package client

import (
	"context"
	"encoding/json"

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
	var questions []string
	if resp != nil && resp.StatusMessage != "" {
		_ = json.Unmarshal([]byte(resp.StatusMessage), &questions)
	}
	return questions, nil
}

func (c *QuestionClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}
