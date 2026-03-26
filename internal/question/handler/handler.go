package handler

import (
	common "Noah/kitex_gen/common"
	question "Noah/kitex_gen/question"
	"context"
)

// QAServiceImpl implements the last service interface defined in the IDL.
type QAServiceImpl struct{}

// AskQuestion implements the QAServiceImpl interface.
func (s *QAServiceImpl) AskQuestion(ctx context.Context, req *question.QuestionReq) (resp *question.AnswerResp, err error) {
	// TODO: Your code here...
	return
}

// BatchAskQuestions implements the QAServiceImpl interface.
func (s *QAServiceImpl) BatchAskQuestions(ctx context.Context, req *question.BatchQuestionReq) (resp *question.BatchAnswerResp, err error) {
	// TODO: Your code here...
	return
}

// GetHistory implements the QAServiceImpl interface.
func (s *QAServiceImpl) GetHistory(ctx context.Context, req *question.HistoryReq) (resp *question.HistoryResp, err error) {
	// TODO: Your code here...
	return
}

// SubmitFeedback implements the QAServiceImpl interface.
func (s *QAServiceImpl) SubmitFeedback(ctx context.Context, req *question.FeedbackReq) (resp *common.BaseResp, err error) {
	// TODO: Your code here...
	return
}

// GetRecommendedQuestions implements the QAServiceImpl interface.
func (s *QAServiceImpl) GetRecommendedQuestions(ctx context.Context, userId int64) (resp *common.BaseResp, err error) {
	// TODO: Your code here...
	return
}
