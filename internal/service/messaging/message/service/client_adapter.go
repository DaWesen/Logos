package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"Logos/pkg/client"
)

// ErrDegradedAnswer 表示问答服务只返回了兜底文案，模型并没有真正回答。
var ErrDegradedAnswer = errors.New("问答服务返回降级答案")

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
	result, err := a.QuestionClient.AskQuestionWithRequestID(ctx, question, uid, requestID)
	if err != nil {
		return nil, err
	}
	if result.Degraded {
		// 降级答案不能当成处理成功：一旦在这里返回 nil，消息会被标记
		// PROCESSED，用户永远等不到真正的回答。返回错误交给 Kafka 重试；
		// 问答侧的降级记录允许被下一次成功回答覆盖，所以重试不会拿到
		// 同一段兜底文案后原地打转。
		return nil, fmt.Errorf("%w: question_id=%s", ErrDegradedAnswer, result.QuestionID)
	}
	return &idWrapper{id: "question_asked"}, nil
}

type idWrapper struct {
	id string
}

func (w *idWrapper) GetID() string { return w.id }
