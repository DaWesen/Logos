package service

import (
	"context"

	"Logos/pkg/client"
)

type QuestionClientAdapter struct {
	*client.QuestionClient
}

func NewQuestionClientAdapter(c *client.QuestionClient) *QuestionClientAdapter {
	return &QuestionClientAdapter{QuestionClient: c}
}

func (a *QuestionClientAdapter) AskQuestion(ctx context.Context, question string, userID *string) (interface{ GetID() string }, error) {
	var uid int64
	if userID != nil {
		return nil, nil
	}
	_, err := a.QuestionClient.AskQuestion(ctx, question, uid)
	if err != nil {
		return nil, err
	}
	return &idWrapper{id: "question_asked"}, nil
}

type idWrapper struct {
	id string
}

func (w *idWrapper) GetID() string { return w.id }
