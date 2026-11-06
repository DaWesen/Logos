package service

import (
	"context"
	"fmt"
	"strconv"

	"Logos/pkg/client"
)

type QuestionClientAdapter struct {
	*client.QuestionClient
}

func NewQuestionClientAdapter(c *client.QuestionClient) *QuestionClientAdapter {
	return &QuestionClientAdapter{QuestionClient: c}
}

func (a *QuestionClientAdapter) AskQuestion(
	ctx context.Context,
	question string,
	userID *string,
	requestID string,
) (interface{ GetID() string }, error) {
	var uid int64
	if userID != nil {
		parsed, err := strconv.ParseInt(*userID, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("解析问答用户 ID 失败: %w", err)
		}
		uid = parsed
	}
	_, err := a.QuestionClient.AskQuestionWithRequestID(ctx, question, uid, requestID)
	if err != nil {
		return nil, err
	}
	return &idWrapper{id: "question_asked"}, nil
}

type idWrapper struct {
	id string
}

func (w *idWrapper) GetID() string { return w.id }
