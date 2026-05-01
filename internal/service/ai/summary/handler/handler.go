package handler

import (
	"context"
	"time"

	"Logos/internal/service/ai/summary/service"
	"Logos/pkg/logger"
	pb "Logos/proto_gen/summary"

	"google.golang.org/protobuf/types/known/timestamppb"
)

type SummaryServiceImpl struct {
	pb.UnimplementedSummaryServiceServer
	SummaryService service.SummaryService
}

func (s *SummaryServiceImpl) SummarizeMessages(ctx context.Context, req *pb.SummarizeMessagesRequest) (*pb.SummarizeMessagesResponse, error) {
	resp := &pb.SummarizeMessagesResponse{}

	var messageIDs []string
	if len(req.MessageIds) > 0 {
		messageIDs = req.MessageIds
	}

	chatType := ""
	if req.ChatType != 0 {
		chatType = req.ChatType.String()
	}

	record, todos, candidates, keyPoints, participants, err := s.SummaryService.SummarizeMessages(
		ctx, req.ChatId, chatType, messageIDs, req.IncludeTodos, req.IncludeCandidates,
	)
	if err != nil {
		logger.Error("总结消息失败", logger.ErrorField(err))
		resp.Code = 1
		resp.Message = err.Error()
		return resp, nil
	}

	resp.Code = 0
	resp.Message = "success"
	if record != nil {
		resp.Summary = record.Summary
	}

	for _, kp := range keyPoints {
		resp.KeyPoints = append(resp.KeyPoints, kp)
	}
	for _, p := range participants {
		resp.Participants = append(resp.Participants, p)
	}
	for _, todo := range todos {
		resp.Todos = append(resp.Todos, &pb.TodoItem{
			Id:       todo.ID,
			Content:  todo.Content,
			Assignee: todo.Assignee,
			Status:   todo.Status,
		})
		if todo.Deadline != "" {
			if t, err := time.Parse(time.RFC3339, todo.Deadline); err == nil {
				resp.Todos[len(resp.Todos)-1].Deadline = timestamppb.New(t)
			}
		}
	}
	for _, c := range candidates {
		resp.ReplyCandidates = append(resp.ReplyCandidates, &pb.ReplyCandidate{
			Id:         c.ID,
			Content:    c.Content,
			Confidence: float32(c.Confidence),
			Type:       c.Type,
		})
	}

	return resp, nil
}

func (s *SummaryServiceImpl) GenerateReplyCandidates(ctx context.Context, req *pb.GenerateReplyCandidatesRequest) (*pb.GenerateReplyCandidatesResponse, error) {
	resp := &pb.GenerateReplyCandidatesResponse{}

	chatType := ""
	if req.ChatType != 0 {
		chatType = req.ChatType.String()
	}

	candidateCount := int(req.CandidateCount)
	if candidateCount <= 0 {
		candidateCount = 3
	}

	candidates, err := s.SummaryService.GenerateReplyCandidates(
		ctx, req.ChatId, chatType, req.ContextMessageIds, candidateCount, req.Tone,
	)
	if err != nil {
		logger.Error("生成回复候选失败", logger.ErrorField(err))
		resp.Code = 1
		resp.Message = err.Error()
		return resp, nil
	}

	resp.Code = 0
	resp.Message = "success"
	for _, c := range candidates {
		resp.Candidates = append(resp.Candidates, &pb.ReplyCandidate{
			Id:         c.ID,
			Content:    c.Content,
			Confidence: float32(c.Confidence),
			Type:       c.Type,
		})
	}

	return resp, nil
}

func (s *SummaryServiceImpl) ExtractTodos(ctx context.Context, req *pb.ExtractTodosRequest) (*pb.ExtractTodosResponse, error) {
	resp := &pb.ExtractTodosResponse{}

	chatType := ""
	if req.ChatType != 0 {
		chatType = req.ChatType.String()
	}

	todos, err := s.SummaryService.ExtractTodos(ctx, req.ChatId, chatType, req.MessageIds)
	if err != nil {
		logger.Error("提取待办事项失败", logger.ErrorField(err))
		resp.Code = 1
		resp.Message = err.Error()
		return resp, nil
	}

	resp.Code = 0
	resp.Message = "success"
	for _, todo := range todos {
		resp.Todos = append(resp.Todos, &pb.TodoItem{
			Id:       todo.ID,
			Content:  todo.Content,
			Assignee: todo.Assignee,
			Status:   todo.Status,
		})
		if todo.Deadline != "" {
			if t, err := time.Parse(time.RFC3339, todo.Deadline); err == nil {
				resp.Todos[len(resp.Todos)-1].Deadline = timestamppb.New(t)
			}
		}
	}

	return resp, nil
}
