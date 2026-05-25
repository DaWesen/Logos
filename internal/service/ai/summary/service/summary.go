package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"Logos/internal/service/ai/summary/dao"
	summarymodel "Logos/internal/service/ai/summary/model"
	"Logos/pkg/eino"
	"Logos/pkg/logger"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
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

type ModelConfig struct {
	Provider    string
	Model       string
	ApiKey      string
	BaseUrl     string
	Temperature float64
}

type SummaryService interface {
	SummarizeMessages(ctx context.Context, chatID string, chatType string, messageIDs []string, includeTodos bool, includeCandidates bool, msgCount int, cfg *ModelConfig) (*summarymodel.SummaryRecord, []TodoItem, []ReplyCandidate, []string, []string, error)
	GenerateReplyCandidates(ctx context.Context, chatID string, chatType string, contextMessageIDs []string, candidateCount int, tone string, cfg *ModelConfig) ([]ReplyCandidate, error)
	ExtractTodos(ctx context.Context, chatID string, chatType string, messageIDs []string, msgCount int, cfg *ModelConfig) ([]TodoItem, error)
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

func (s *summaryServiceImpl) getChatModel(cfg *ModelConfig) (einomodel.BaseChatModel, error) {
	if cfg != nil && cfg.ApiKey != "" && cfg.Model != "" {
		baseURL := cfg.BaseUrl
		if baseURL == "" {
			baseURL = defaultBaseURL(cfg.Provider)
		}
		logger.Info("使用动态ChatModel",
			logger.StringField("provider", cfg.Provider),
			logger.StringField("model", cfg.Model),
			logger.StringField("base_url", baseURL))
		return eino.NewDynamicChatModel(cfg.ApiKey, cfg.Model, baseURL)
	}
	if s.einoClient != nil && s.einoClient.HasChatModel() {
		return s.einoClient.GetChatModel(), nil
	}
	return nil, fmt.Errorf("无可用的 ChatModel，请在设置中配置 AI 模型或检查服务端配置")
}

func defaultBaseURL(provider string) string {
	switch provider {
	case "deepseek":
		return "https://api.deepseek.com/v1"
	case "qianfan":
		return "https://aip.baidubce.com/rpc/2.0/ai_custom/v1"
	case "claude", "anthropic":
		return "https://api.anthropic.com/v1"
	default:
		return "https://api.openai.com/v1"
	}
}

func (s *summaryServiceImpl) chatWithModel(ctx context.Context, chatModel einomodel.BaseChatModel, systemPrompt string, userPrompt string) (string, error) {
	messages := []*schema.Message{
		schema.SystemMessage(systemPrompt),
		schema.UserMessage(userPrompt),
	}
	aiCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	resp, err := chatModel.Generate(aiCtx, messages)
	if err != nil {
		return "", err
	}
	return resp.Content, nil
}

func (s *summaryServiceImpl) getMessageHistoryWithTimeout(chatID string, limit int) ([]*ChatMessage, error) {
	if s.chatService == nil {
		return nil, fmt.Errorf("chatService 未配置")
	}
	historyCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return s.chatService.GetMessageHistory(historyCtx, chatID, limit, nil)
}

func (s *summaryServiceImpl) SummarizeMessages(ctx context.Context, chatID string, chatType string, messageIDs []string, includeTodos bool, includeCandidates bool, msgCount int, cfg *ModelConfig) (*summarymodel.SummaryRecord, []TodoItem, []ReplyCandidate, []string, []string, error) {
	logger.Info("总结消息", logger.StringField("chat_id", chatID))

	limit := 100
	if msgCount > 0 {
		limit = msgCount
	}

	var messagesText string
	if len(messageIDs) == 0 {
		msgs, err := s.getMessageHistoryWithTimeout(chatID, limit)
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

	chatModel, err := s.getChatModel(cfg)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("获取ChatModel失败: %w", err)
	}

	summary, err := s.generateSummaryWithModel(ctx, chatModel, messagesText)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("生成摘要失败: %w", err)
	}

	var keyPoints []string
	kp, err := s.extractKeyPointsWithModel(ctx, chatModel, messagesText)
	if err == nil {
		keyPoints = kp
	}

	var participants []string
	parts, err := s.extractParticipantsWithModel(ctx, chatModel, messagesText)
	if err == nil {
		participants = parts
	}

	var todos []TodoItem
	if includeTodos {
		todos, _ = s.extractTodosFromTextWithModel(ctx, chatModel, messagesText)
	}

	var candidates []ReplyCandidate
	if includeCandidates {
		candidates, _ = s.generateCandidatesWithModel(ctx, chatModel, messagesText, 3, "friendly")
	}

	record := &summarymodel.SummaryRecord{
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

func (s *summaryServiceImpl) GenerateReplyCandidates(ctx context.Context, chatID string, chatType string, contextMessageIDs []string, candidateCount int, tone string, cfg *ModelConfig) ([]ReplyCandidate, error) {
	logger.Info("生成回复候选", logger.StringField("chat_id", chatID))

	var contextText string
	msgs, err := s.getMessageHistoryWithTimeout(chatID, 20)
	if err == nil {
		for _, msg := range msgs {
			contextText += fmt.Sprintf("[%s]: %s\n", msg.SenderID, msg.Content)
		}
	}

	if contextText == "" {
		contextText = fmt.Sprintf("聊天ID: %s (类型: %s)", chatID, chatType)
	}

	chatModel, err := s.getChatModel(cfg)
	if err != nil {
		return nil, fmt.Errorf("获取ChatModel失败: %w", err)
	}

	return s.generateCandidatesWithModel(ctx, chatModel, contextText, candidateCount, tone)
}

func (s *summaryServiceImpl) ExtractTodos(ctx context.Context, chatID string, chatType string, messageIDs []string, msgCount int, cfg *ModelConfig) ([]TodoItem, error) {
	logger.Info("提取待办事项", logger.StringField("chat_id", chatID))

	limit := 50
	if msgCount > 0 {
		limit = msgCount
	}

	var messagesText string
	msgs, err := s.getMessageHistoryWithTimeout(chatID, limit)
	if err == nil {
		for _, msg := range msgs {
			messagesText += fmt.Sprintf("[%s]: %s\n", msg.SenderID, msg.Content)
		}
	}

	if messagesText == "" {
		messagesText = fmt.Sprintf("聊天ID: %s (类型: %s)", chatID, chatType)
	}

	chatModel, err := s.getChatModel(cfg)
	if err != nil {
		return nil, fmt.Errorf("获取ChatModel失败: %w", err)
	}

	return s.extractTodosFromTextWithModel(ctx, chatModel, messagesText)
}

func (s *summaryServiceImpl) generateSummaryWithModel(ctx context.Context, chatModel einomodel.BaseChatModel, text string) (string, error) {
	prompt := fmt.Sprintf(`请对以下聊天记录生成简洁摘要，包含主要讨论话题、关键决策和结论。
直接输出摘要内容，不要任何前缀或解释。

聊天记录：
%s`, text)

	return s.chatWithModel(ctx, chatModel, "你是一个专业的聊天记录摘要生成系统。", prompt)
}

func (s *summaryServiceImpl) extractKeyPointsWithModel(ctx context.Context, chatModel einomodel.BaseChatModel, text string) ([]string, error) {
	prompt := fmt.Sprintf(`请从以下聊天记录中提取关键要点，以JSON字符串数组格式输出。
只输出JSON数组，不要其他内容。

聊天记录：
%s`, text)

	response, err := s.chatWithModel(ctx, chatModel, "你是一个专业的信息提取系统。", prompt)
	if err != nil {
		return nil, err
	}

	var points []string
	if err := json.Unmarshal([]byte(trimJSON(response)), &points); err != nil {
		return []string{response}, nil
	}
	return points, nil
}

func (s *summaryServiceImpl) extractParticipantsWithModel(ctx context.Context, chatModel einomodel.BaseChatModel, text string) ([]string, error) {
	prompt := fmt.Sprintf(`请从以下聊天记录中提取所有参与者的用户名或ID，以JSON字符串数组格式输出。
只输出JSON数组，不要其他内容。

聊天记录：
%s`, text)

	response, err := s.chatWithModel(ctx, chatModel, "你是一个专业的信息提取系统。", prompt)
	if err != nil {
		return nil, err
	}

	var participants []string
	if err := json.Unmarshal([]byte(trimJSON(response)), &participants); err != nil {
		return []string{}, nil
	}
	return participants, nil
}

func (s *summaryServiceImpl) extractTodosFromTextWithModel(ctx context.Context, chatModel einomodel.BaseChatModel, text string) ([]TodoItem, error) {
	prompt := fmt.Sprintf(`请从以下聊天记录中提取所有待办事项，以JSON数组格式输出，每个对象包含：
- "id": 唯一标识
- "content": 待办内容
- "assignee": 负责人
- "deadline": 截止时间（如有）
- "status": 状态（pending/in_progress/completed）
只输出JSON数组，不要其他内容。

聊天记录：
%s`, text)

	response, err := s.chatWithModel(ctx, chatModel, "你是一个专业的待办事项提取系统。", prompt)
	if err != nil {
		return nil, err
	}

	var todos []TodoItem
	if err := json.Unmarshal([]byte(trimJSON(response)), &todos); err != nil {
		return []TodoItem{}, nil
	}
	return todos, nil
}

func (s *summaryServiceImpl) generateCandidatesWithModel(ctx context.Context, chatModel einomodel.BaseChatModel, contextText string, count int, tone string) ([]ReplyCandidate, error) {
	prompt := fmt.Sprintf(`根据以下聊天上下文，以"我"的视角生成%d条%s语气的回复候选。

要求：
1. 你是聊天中最后一条消息的接收者，请站在"我"的角度直接回复对方
2. 回复要自然、口语化，像真人聊天一样，不要使用"对方"这样的第三人称
3. 根据上下文理解对话关系和语气，保持一致性
4. 以JSON数组格式输出，每个对象包含：
   - "id": 唯一标识
   - "content": 回复内容
   - "confidence": 置信度（0.0-1.0）
   - "type": 回复类型（如agreement/question_suggestion等）
只输出JSON数组，不要其他内容。

聊天上下文：
%s`, count, tone, contextText)

	response, err := s.chatWithModel(ctx, chatModel, "你是一个智能回复建议系统。请站在用户的角度，帮助用户生成自然、得体的回复。", prompt)
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
