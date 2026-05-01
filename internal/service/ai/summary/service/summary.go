package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"Logos/internal/service/ai/summary/dao"
	"Logos/internal/service/ai/summary/model"
	"Logos/pkg/eino"
	"Logos/pkg/logger"

	"github.com/google/uuid"
)

type ChatService interface {
	GetMessageHistory(ctx context.Context, chatID string, limit int, beforeTime *time.Time) ([]*ChatMessage, error)
}

type ChatMessage struct {
	ID        string
	ChatID    string
	SenderID  string
	Content   string
	CreatedAt time.Time
}

type SummaryService interface {
	SummarizeMessages(ctx context.Context, chatID string, chatType string, messageIDs []string, includeTodos bool, includeCandidates bool) (*model.SummaryRecord, []TodoItem, []ReplyCandidate, []string, []string, error)
	GenerateReplyCandidates(ctx context.Context, chatID string, chatType string, contextMessageIDs []string, candidateCount int, tone string) ([]ReplyCandidate, error)
	ExtractTodos(ctx context.Context, chatID string, chatType string, messageIDs []string) ([]TodoItem, error)
}

type TodoItem struct {
	ID       string `json:"id"`
	Content  string `json:"content"`
	Assignee string `json:"assignee"`
	Deadline string `json:"deadline"`
	Status   string `json:"status"`
}

type ReplyCandidate struct {
	ID         string  `json:"id"`
	Content    string  `json:"content"`
	Confidence float64 `json:"confidence"`
	Type       string  `json:"type"`
}

type summaryServiceImpl struct {
	repo        dao.SummaryRepository
	einoClient  *eino.EinoManager
	chatService ChatService
}

func NewSummaryService(repo dao.SummaryRepository, einoClient *eino.EinoManager, chatService ChatService) SummaryService {
	return &summaryServiceImpl{repo: repo, einoClient: einoClient, chatService: chatService}
}

func (s *summaryServiceImpl) SummarizeMessages(ctx context.Context, chatID string, chatType string, messageIDs []string, includeTodos bool, includeCandidates bool) (*model.SummaryRecord, []TodoItem, []ReplyCandidate, []string, []string, error) {
	logger.Info("总结消息", logger.StringField("chat_id", chatID))

	var messagesText string
	if s.chatService != nil && len(messageIDs) == 0 {
		msgs, err := s.chatService.GetMessageHistory(ctx, chatID, 100, nil)
		if err != nil {
			logger.Warn("获取消息历史失败", logger.ErrorField(err))
		} else {
			for _, msg := range msgs {
				messagesText += fmt.Sprintf("[%s]: %s\n", msg.SenderID, msg.Content)
			}
		}
	}

	if messagesText == "" {
		messagesText = fmt.Sprintf("聊天ID: %s (类型: %s)", chatID, chatType)
	}

	summary, err := s.generateSummary(ctx, messagesText)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("生成摘要失败: %w", err)
	}

	var keyPoints []string
	kp, err := s.extractKeyPoints(ctx, messagesText)
	if err == nil {
		keyPoints = kp
	}

	var participants []string
	parts, err := s.extractParticipants(ctx, messagesText)
	if err == nil {
		participants = parts
	}

	var todos []TodoItem
	if includeTodos {
		todos, _ = s.extractTodosFromText(ctx, messagesText)
	}

	var candidates []ReplyCandidate
	if includeCandidates {
		candidates, _ = s.generateCandidates(ctx, messagesText, 3, "friendly")
	}

	record := &model.SummaryRecord{
		ID:           uuid.New().String(),
		ChatID:       chatID,
		ChatType:     chatType,
		Summary:      summary,
		KeyPoints:    mustMarshal(keyPoints),
		Participants: mustMarshal(participants),
		Todos:        mustMarshal(todos),
		MessageIDs:   mustMarshal(messageIDs),
	}

	if err := s.repo.CreateSummary(ctx, record); err != nil {
		logger.Error("保存摘要记录失败", logger.ErrorField(err))
	}

	return record, todos, candidates, keyPoints, participants, nil
}

func (s *summaryServiceImpl) GenerateReplyCandidates(ctx context.Context, chatID string, chatType string, contextMessageIDs []string, candidateCount int, tone string) ([]ReplyCandidate, error) {
	logger.Info("生成回复候选", logger.StringField("chat_id", chatID))

	var contextText string
	if s.chatService != nil {
		msgs, err := s.chatService.GetMessageHistory(ctx, chatID, 20, nil)
		if err == nil {
			for _, msg := range msgs {
				contextText += fmt.Sprintf("[%s]: %s\n", msg.SenderID, msg.Content)
			}
		}
	}

	if contextText == "" {
		contextText = fmt.Sprintf("聊天ID: %s (类型: %s)", chatID, chatType)
	}

	return s.generateCandidates(ctx, contextText, candidateCount, tone)
}

func (s *summaryServiceImpl) ExtractTodos(ctx context.Context, chatID string, chatType string, messageIDs []string) ([]TodoItem, error) {
	logger.Info("提取待办事项", logger.StringField("chat_id", chatID))

	var messagesText string
	if s.chatService != nil {
		msgs, err := s.chatService.GetMessageHistory(ctx, chatID, 50, nil)
		if err == nil {
			for _, msg := range msgs {
				messagesText += fmt.Sprintf("[%s]: %s\n", msg.SenderID, msg.Content)
			}
		}
	}

	if messagesText == "" {
		messagesText = fmt.Sprintf("聊天ID: %s (类型: %s)", chatID, chatType)
	}

	return s.extractTodosFromText(ctx, messagesText)
}

func (s *summaryServiceImpl) generateSummary(ctx context.Context, text string) (string, error) {
	if s.einoClient == nil || !s.einoClient.HasChatModel() {
		return "摘要服务暂不可用", nil
	}

	prompt := fmt.Sprintf(`请对以下聊天记录生成简洁摘要，包含主要讨论话题、关键决策和结论。
直接输出摘要内容，不要任何前缀或解释。

聊天记录：
%s`, text)

	response, err := s.einoClient.Chat(ctx, []string{
		"你是一个专业的聊天记录摘要生成系统。",
		prompt,
	})
	if err != nil {
		return "", fmt.Errorf("LLM摘要生成失败: %w", err)
	}

	return response, nil
}

func (s *summaryServiceImpl) extractKeyPoints(ctx context.Context, text string) ([]string, error) {
	if s.einoClient == nil || !s.einoClient.HasChatModel() {
		return []string{}, nil
	}

	prompt := fmt.Sprintf(`请从以下聊天记录中提取关键要点，以JSON字符串数组格式输出。
只输出JSON数组，不要其他内容。

聊天记录：
%s`, text)

	response, err := s.einoClient.Chat(ctx, []string{
		"你是一个专业的信息提取系统。",
		prompt,
	})
	if err != nil {
		return nil, err
	}

	var points []string
	if err := json.Unmarshal([]byte(trimJSON(response)), &points); err != nil {
		return []string{response}, nil
	}
	return points, nil
}

func (s *summaryServiceImpl) extractParticipants(ctx context.Context, text string) ([]string, error) {
	if s.einoClient == nil || !s.einoClient.HasChatModel() {
		return []string{}, nil
	}

	prompt := fmt.Sprintf(`请从以下聊天记录中提取所有参与者的用户名或ID，以JSON字符串数组格式输出。
只输出JSON数组，不要其他内容。

聊天记录：
%s`, text)

	response, err := s.einoClient.Chat(ctx, []string{
		"你是一个专业的信息提取系统。",
		prompt,
	})
	if err != nil {
		return nil, err
	}

	var participants []string
	if err := json.Unmarshal([]byte(trimJSON(response)), &participants); err != nil {
		return []string{}, nil
	}
	return participants, nil
}

func (s *summaryServiceImpl) extractTodosFromText(ctx context.Context, text string) ([]TodoItem, error) {
	if s.einoClient == nil || !s.einoClient.HasChatModel() {
		return []TodoItem{}, nil
	}

	prompt := fmt.Sprintf(`请从以下聊天记录中提取所有待办事项，以JSON数组格式输出，每个对象包含：
- "id": 唯一标识
- "content": 待办内容
- "assignee": 负责人
- "deadline": 截止时间（如有）
- "status": 状态（pending/in_progress/completed）
只输出JSON数组，不要其他内容。

聊天记录：
%s`, text)

	response, err := s.einoClient.Chat(ctx, []string{
		"你是一个专业的待办事项提取系统。",
		prompt,
	})
	if err != nil {
		return nil, err
	}

	var todos []TodoItem
	if err := json.Unmarshal([]byte(trimJSON(response)), &todos); err != nil {
		return []TodoItem{}, nil
	}
	return todos, nil
}

func (s *summaryServiceImpl) generateCandidates(ctx context.Context, contextText string, count int, tone string) ([]ReplyCandidate, error) {
	if s.einoClient == nil || !s.einoClient.HasChatModel() {
		return []ReplyCandidate{}, nil
	}

	prompt := fmt.Sprintf(`根据以下聊天上下文，生成%d条%s语气的回复候选，以JSON数组格式输出，每个对象包含：
- "id": 唯一标识
- "content": 回复内容
- "confidence": 置信度（0.0-1.0）
- "type": 回复类型（如agreement/question_suggestion等）
只输出JSON数组，不要其他内容。

聊天上下文：
%s`, count, tone, contextText)

	response, err := s.einoClient.Chat(ctx, []string{
		"你是一个智能回复建议系统。",
		prompt,
	})
	if err != nil {
		return nil, err
	}

	var candidates []ReplyCandidate
	if err := json.Unmarshal([]byte(trimJSON(response)), &candidates); err != nil {
		return []ReplyCandidate{}, nil
	}
	return candidates, nil
}

func trimJSON(s string) string {
	start := -1
	for i := 0; i < len(s); i++ {
		if s[i] == '[' || s[i] == '{' {
			start = i
			break
		}
	}
	if start < 0 {
		return s
	}
	end := -1
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == ']' || s[i] == '}' {
			end = i
			break
		}
	}
	if end < 0 {
		return s[start:]
	}
	return s[start : end+1]
}

func mustMarshal(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}
