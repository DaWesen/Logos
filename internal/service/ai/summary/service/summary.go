package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"Logos/internal/service/ai/summary/dao"
	summarymodel "Logos/internal/service/ai/summary/model"
	"Logos/pkg/eino"
	"Logos/pkg/logger"
	"Logos/pkg/structured"

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
	// newChatModel 是模型构造的唯一入口：生产环境走 buildChatModel，
	// 测试注入脚本化模型，避免为了写单测去 mock 整个 EinoManager。
	newChatModel func(cfg *ModelConfig) (einomodel.BaseChatModel, error)
}

func NewSummaryService(repo dao.SummaryRepository, einoClient *eino.EinoManager, chatService ChatService) SummaryService {
	s := &summaryServiceImpl{repo: repo, einoClient: einoClient, chatService: chatService}
	s.newChatModel = s.buildChatModel
	return s
}

func (s *summaryServiceImpl) getChatModel(cfg *ModelConfig) (einomodel.BaseChatModel, error) {
	if s.newChatModel != nil {
		return s.newChatModel(cfg)
	}
	return s.buildChatModel(cfg)
}

func (s *summaryServiceImpl) buildChatModel(cfg *ModelConfig) (einomodel.BaseChatModel, error) {
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

func (s *summaryServiceImpl) getMessageHistoryWithTimeout(ctx context.Context, chatID string, limit int) ([]*ChatMessage, error) {
	if s.chatService == nil {
		return nil, fmt.Errorf("chatService 未配置")
	}
	// 使用调用方 ctx：总结/候选/待办都是请求内操作，客户端断开后
	// 不应继续拉取历史并占用 Chat 服务的连接。
	historyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return s.chatService.GetMessageHistory(historyCtx, chatID, limit, nil)
}

// loadMessageText 统一拉取并格式化最近历史。
//
// 这里刻意不做「拉取失败就用聊天 ID 占位」的降级：占位串会让模型生成一份
// 看起来正常、实际没有任何依据的摘要，而调用方拿到的是 200 成功。
// 历史为空同样返回错误——没有内容的总结没有业务意义。
func (s *summaryServiceImpl) loadMessageText(ctx context.Context, chatID string, limit int, requestedIDs []string) (string, error) {
	if len(requestedIDs) > 0 {
		// Chat 服务没有提供按 ID 批量取消息的接口，显式记录降级原因，
		// 避免「传了 message_ids 却总结最近历史」被当成正常行为。
		logger.Warn("暂不支持按消息 ID 取内容，回退到最近历史",
			logger.StringField("chat_id", chatID),
			logger.IntField("requested_id_count", len(requestedIDs)))
	}

	msgs, err := s.getMessageHistoryWithTimeout(ctx, chatID, limit)
	if err != nil {
		return "", fmt.Errorf("拉取消息历史失败: %w", err)
	}

	var builder strings.Builder
	used := 0
	for _, msg := range msgs {
		if msg == nil || strings.TrimSpace(msg.Content) == "" {
			continue
		}
		builder.WriteString(fmt.Sprintf("[%s]: %s\n", msg.SenderID, msg.Content))
		used++
	}
	if used == 0 {
		return "", fmt.Errorf("聊天 %s 没有可用的消息内容（最近 %d 条为空）", chatID, limit)
	}

	logger.Info("加载总结上下文完成",
		logger.StringField("chat_id", chatID),
		logger.IntField("message_count", used))
	return builder.String(), nil
}

func (s *summaryServiceImpl) SummarizeMessages(ctx context.Context, chatID string, chatType string, messageIDs []string, includeTodos bool, includeCandidates bool, msgCount int, cfg *ModelConfig) (*summarymodel.SummaryRecord, []TodoItem, []ReplyCandidate, []string, []string, error) {
	logger.Info("总结消息", logger.StringField("chat_id", chatID))

	limit := 100
	if msgCount > 0 {
		limit = msgCount
	}

	messagesText, err := s.loadMessageText(ctx, chatID, limit, messageIDs)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}

	chatModel, err := s.getChatModel(cfg)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("获取ChatModel失败: %w", err)
	}

	summary, err := s.generateSummaryWithModel(ctx, chatModel, messagesText)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("生成摘要失败: %w", err)
	}

	keyPoints, kpErr := s.extractKeyPointsWithModel(ctx, chatModel, messagesText)
	if kpErr != nil {
		// 要点是锦上添花：保留摘要本体，但必须把降级原因写进日志，
		// 不能让「模型/解析故障」和「确实没有要点」看起来一样。
		logger.Error("提取关键要点失败，本次结果不含要点",
			logger.StringField("chat_id", chatID),
			logger.ErrorField(kpErr))
		keyPoints = nil
	}

	participants, pErr := s.extractParticipantsWithModel(ctx, chatModel, messagesText)
	if pErr != nil {
		logger.Error("提取参与者失败，本次结果不含参与者",
			logger.StringField("chat_id", chatID),
			logger.ErrorField(pErr))
		participants = nil
	}

	var todos []TodoItem
	if includeTodos {
		todos, err = s.extractTodosFromTextWithModel(ctx, chatModel, messagesText)
		if err != nil {
			return nil, nil, nil, nil, nil, fmt.Errorf("提取待办事项失败: %w", err)
		}
	}

	var candidates []ReplyCandidate
	if includeCandidates {
		candidates, err = s.generateCandidatesWithModel(ctx, chatModel, messagesText, 3, "friendly")
		if err != nil {
			return nil, nil, nil, nil, nil, fmt.Errorf("生成回复候选失败: %w", err)
		}
	}

	record := &summarymodel.SummaryRecord{
		ID:       uuid.New().String(),
		ChatID:   chatID,
		ChatType: chatType,
		Summary:  summary,
	}

	fields := []struct {
		name  string
		value interface{}
		dst   *string
	}{
		{"key_points", keyPoints, &record.KeyPoints},
		{"participants", participants, &record.Participants},
		{"todos", todos, &record.Todos},
		{"message_ids", messageIDs, &record.MessageIDs},
	}
	for _, field := range fields {
		encoded, err := marshalField(field.name, field.value)
		if err != nil {
			return nil, nil, nil, nil, nil, err
		}
		*field.dst = encoded
	}

	if err := s.repo.CreateSummary(ctx, record); err != nil {
		// 记录没落库就不能返回成功：否则调用方拿着摘要去展示，
		// 摘要历史里却查不到这条记录，事后无法复盘。
		return nil, nil, nil, nil, nil, fmt.Errorf("保存摘要记录失败: %w", err)
	}

	return record, todos, candidates, keyPoints, participants, nil
}

func (s *summaryServiceImpl) GenerateReplyCandidates(ctx context.Context, chatID string, chatType string, contextMessageIDs []string, candidateCount int, tone string, cfg *ModelConfig) ([]ReplyCandidate, error) {
	logger.Info("生成回复候选", logger.StringField("chat_id", chatID))

	contextText, err := s.loadMessageText(ctx, chatID, 20, contextMessageIDs)
	if err != nil {
		return nil, err
	}

	chatModel, err := s.getChatModel(cfg)
	if err != nil {
		return nil, fmt.Errorf("获取ChatModel失败: %w", err)
	}

	candidates, err := s.generateCandidatesWithModel(ctx, chatModel, contextText, candidateCount, tone)
	if err != nil {
		return nil, fmt.Errorf("生成回复候选失败: %w", err)
	}
	return candidates, nil
}

func (s *summaryServiceImpl) ExtractTodos(ctx context.Context, chatID string, chatType string, messageIDs []string, msgCount int, cfg *ModelConfig) ([]TodoItem, error) {
	logger.Info("提取待办事项", logger.StringField("chat_id", chatID))

	limit := 50
	if msgCount > 0 {
		limit = msgCount
	}

	messagesText, err := s.loadMessageText(ctx, chatID, limit, messageIDs)
	if err != nil {
		return nil, err
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
	if err := s.parseStructured(ctx, chatModel, "summary", `["要点1","要点2"]`, response, &points); err != nil {
		logger.Warn("关键要点解析失败，按纯文本处理",
			logger.ErrorField(err))
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
	if err := s.parseStructured(ctx, chatModel, "summary", `["user1","user2"]`, response, &participants); err != nil {
		return nil, err
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
	if err := s.parseStructured(ctx, chatModel, "summary",
		`[{"id":"...","content":"...","assignee":"...","deadline":"...","status":"pending|in_progress|completed"}]`,
		response, &todos); err != nil {
		return nil, err
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
	if err := s.parseStructured(ctx, chatModel, "summary",
		`[{"id":"...","content":"...","confidence":0.0,"type":"..."}]`,
		response, &candidates); err != nil {
		return nil, err
	}
	return candidates, nil
}

// parseStructured 解析模型输出；失败时带错误信息与期望结构做一次定向修复。
// 四个提取点共用，避免每个方法各自写一套「解析失败怎么办」。
func (s *summaryServiceImpl) parseStructured(
	ctx context.Context, chatModel einomodel.BaseChatModel, module, schemaHint, raw string, out interface{},
) error {
	_, err := structured.ParseWithRepair(ctx, module, raw, out,
		func(ctx context.Context, raw string, parseErr error) (string, error) {
			return s.chatWithModel(ctx, chatModel, "你是一个JSON修复助手，只输出修正后的JSON。",
				structured.RepairPrompt(raw, parseErr, schemaHint))
		})
	return err
}

func marshalField(name string, v interface{}) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("序列化 %s 失败: %w", name, err)
	}
	return string(b), nil
}
