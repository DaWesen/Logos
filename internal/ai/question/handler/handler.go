package handler

import (
	"context"
	"time"

	"Logos/internal/ai/question/model"
	"Logos/internal/ai/question/service"
	pb "Logos/proto_gen/question"
	pbCommon "Logos/proto_gen/common"
	"Logos/pkg/logger"
)

type QAServiceImpl struct {
	pb.UnimplementedQAServiceServer
	QAService service.QAService
}

func buildSuccessBaseResp() *pbCommon.BaseResp {
	return &pbCommon.BaseResp{
		StatusCode:    0,
		StatusMessage: "success",
		ServiceTime:   time.Now().Unix(),
	}
}

func buildErrorBaseResp(message string) *pbCommon.BaseResp {
	return &pbCommon.BaseResp{
		StatusCode:    1,
		StatusMessage: message,
		ServiceTime:   time.Now().Unix(),
	}
}

func convertModelQARecordToProtoQARecord(record *model.QARecord) *pb.QARecord {
	result := &pb.QARecord{
		Id:         record.ID,
		Question:   record.Question,
		Answer:     record.Answer,
		Confidence: record.Confidence,
		UserId:     record.UserID,
		Timestamp:  record.Timestamp,
	}
	if record.Feedback != nil {
		result.Feedback = record.Feedback
	}
	if record.Rating != nil {
		result.Rating = record.Rating
	}
	return result
}

// AskQuestion implements the QAServiceImpl interface.
func (s *QAServiceImpl) AskQuestion(ctx context.Context, req *pb.QuestionReq) (*pb.AnswerResp, error) {
	resp := &pb.AnswerResp{}

	var reqContext map[string]string
	if req.Context != nil {
		reqContext = req.Context
	}

	answer, confidence, sources, questionID, timestamp, err := s.QAService.AskQuestion(ctx, req.Content, req.UserId, reqContext)
	if err != nil {
		logger.Error("处理问题失败", logger.ErrorField(err))
		resp.BaseResp = buildErrorBaseResp(err.Error())
		return resp, nil
	}

	resp.BaseResp = buildSuccessBaseResp()
	resp.Answer = answer
	resp.Confidence = confidence
	resp.Sources = sources
	resp.QuestionId = questionID
	resp.Timestamp = timestamp

	return resp, nil
}

// BatchAskQuestions implements the QAServiceImpl interface.
func (s *QAServiceImpl) BatchAskQuestions(ctx context.Context, req *pb.BatchQuestionReq) (*pb.BatchAnswerResp, error) {
	resp := &pb.BatchAnswerResp{}

	answers, err := s.QAService.BatchAskQuestions(ctx, req.Questions, req.UserId)
	if err != nil {
		logger.Error("批量处理问题失败", logger.ErrorField(err))
		resp.BaseResp = buildErrorBaseResp(err.Error())
		return resp, nil
	}

	resp.BaseResp = buildSuccessBaseResp()
	resp.Answers = answers

	return resp, nil
}

// GetHistory implements the QAServiceImpl interface.
func (s *QAServiceImpl) GetHistory(ctx context.Context, req *pb.HistoryReq) (*pb.HistoryResp, error) {
	resp := &pb.HistoryResp{}

	records, total, err := s.QAService.GetHistory(ctx, req.UserId, int(req.Page), int(req.PageSize))
	if err != nil {
		logger.Error("获取问答历史失败", logger.ErrorField(err))
		resp.BaseResp = buildErrorBaseResp(err.Error())
		return resp, nil
	}

	resp.BaseResp = buildSuccessBaseResp()
	resp.Records = make([]*pb.QARecord, 0, len(records))
	for _, record := range records {
		resp.Records = append(resp.Records, convertModelQARecordToProtoQARecord(record))
	}
	resp.Total = total

	return resp, nil
}

// SubmitFeedback implements the QAServiceImpl interface.
func (s *QAServiceImpl) SubmitFeedback(ctx context.Context, req *pb.FeedbackReq) (*pbCommon.BaseResp, error) {
	var rating *int32
	if req.Rating != nil {
		rating = req.Rating
	}

	err := s.QAService.SubmitFeedback(ctx, req.QuestionId, req.Feedback, rating)
	if err != nil {
		logger.Error("提交反馈失败", logger.ErrorField(err))
		return buildErrorBaseResp(err.Error()), nil
	}

	return buildSuccessBaseResp(), nil
}

// GetRecommendedQuestions implements the QAServiceImpl interface.
func (s *QAServiceImpl) GetRecommendedQuestions(ctx context.Context, req *pb.GetRecommendedQuestionsReq) (*pbCommon.BaseResp, error) {
	_, err := s.QAService.GetRecommendedQuestions(ctx, req.UserId)
	if err != nil {
		logger.Error("获取推荐问题失败", logger.ErrorField(err))
		return buildErrorBaseResp(err.Error()), nil
	}

	return buildSuccessBaseResp(), nil
}
