package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"Logos/internal/bot/agent"
	"Logos/internal/bot/provider"
	"Logos/internal/service/ai/bot/dao"
	botmodel "Logos/internal/service/ai/bot/model"
	"Logos/pkg/eino"
	"Logos/pkg/logger"
	"Logos/pkg/mq"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type VectorService interface {
	TextSearch(ctx context.Context, collectionID string, text string, topK int) ([]string, error)
}

type BillingService interface {
	ConsumeModelCall(ctx context.Context, userID string, provider string, modelName string, tokenCount int, metadata map[string]string) (float64, error)
}

var (
	ErrBotNotFound          = errors.New("Bot不存在")
	ErrConversationNotFound = errors.New("对话不存在")
	ErrMessageNotFound      = errors.New("消息不存在")
	ErrPromptNotFound       = errors.New("提示词不存在")
	ErrInternalServer       = errors.New("服务器内部错误")
	ErrAgentNotInitialized  = errors.New("Agent未初始化")
	ErrInsufficientBalance  = errors.New("余额不足")
	ErrInvalidProvider      = errors.New("无效的模型提供商")
)

type BotService interface {
	CreateBot(ctx context.Context, userID, name, description, avatar, botType, modelProvider, modelName, apiKey, baseURL, embeddingModel, systemPrompt string, config map[string]string) (*botmodel.Bot, error)
	UpdateBot(ctx context.Context, userID, id, name, description, avatar, modelName, apiKey, baseURL, embeddingModel, systemPrompt string, config map[string]string, status string) (*botmodel.Bot, error)
	DeleteBot(ctx context.Context, id string) error
	GetBot(ctx context.Context, id string) (*botmodel.Bot, error)
	ListBots(ctx context.Context, userID, botType, status string, page, pageSize int) ([]*botmodel.Bot, int64, error)

	CreateConversation(ctx context.Context, botID, userID, title string) (*botmodel.Conversation, error)
	UpdateConversation(ctx context.Context, id, title string) (*botmodel.Conversation, error)
	DeleteConversation(ctx context.Context, id string) error
	GetConversation(ctx context.Context, id string) (*botmodel.Conversation, error)
	ListConversations(ctx context.Context, botID, userID string, page, pageSize int) ([]*botmodel.Conversation, int64, error)

	SendMessage(ctx context.Context, userID, botID, conversationID, content string, stream bool, metadata map[string]string) (string, error)
	SendMessageStream(ctx context.Context, userID, botID, conversationID, content string, metadata map[string]string, onChunk func(string) error) error
	GetHistory(ctx context.Context, botID, conversationID string, limit int, beforeTime *time.Time) ([]*botmodel.Message, bool, error)

	CreatePrompt(ctx context.Context, userID, name, description, content, promptType string, isPreset, isPublic bool, config map[string]string) (*botmodel.Prompt, error)
	UpdatePrompt(ctx context.Context, id, name, description, content string, isPublic *bool, config map[string]string) (*botmodel.Prompt, error)
	DeletePrompt(ctx context.Context, id string) error
	GetPrompt(ctx context.Context, id string) (*botmodel.Prompt, error)
	ListPrompts(ctx context.Context, userID, promptType string, isPreset, isPublic *bool, page, pageSize int) ([]*botmodel.Prompt, int64, error)

	GetUserMemory(ctx context.Context, userID, botID string) ([]*botmodel.UserMemory, error)
	SetUserMemory(ctx context.Context, userID, botID, key, value string) error

	WithTransaction(ctx context.Context, fn func(txService BotService) error) error
}

type botServiceImpl struct {
	repo           dao.BotRepository
	agentManager   *agent.AgentManager
	einoManager    *eino.EinoManager
	billingService BillingService
	vectorService  VectorService
	producer       *mq.Producer
}

func NewBotService(
	repo dao.BotRepository,
	agentManager *agent.AgentManager,
	einoManager *eino.EinoManager,
	billingService BillingService,
	vectorService VectorService,
	producer *mq.Producer,
) BotService {
	return &botServiceImpl{
		repo:           repo,
		agentManager:   agentManager,
		einoManager:    einoManager,
		billingService: billingService,
		vectorService:  vectorService,
		producer:       producer,
	}
}

func (s *botServiceImpl) CreateBot(ctx context.Context, userID, name, description, avatar, botType, modelProvider, modelName, apiKey, baseURL, embeddingModel, systemPrompt string, config map[string]string) (*botmodel.Bot, error) {
	logger.Info("创建 Bot 请求", logger.StringField("name", name), logger.StringField("userID", userID))

	bot := &botmodel.Bot{
		UserID:         userID,
		Name:           name,
		Description:    description,
		Avatar:         avatar,
		Type:           botType,
		Provider:       modelProvider,
		Model:          modelName,
		APIKey:         apiKey,
		BaseURL:        baseURL,
		EmbeddingModel: embeddingModel,
		SystemPrompt:   systemPrompt,
		Config:         botmodel.JSONMap(config),
		Status:         "active",
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	if err := s.repo.CreateBot(ctx, bot); err != nil {
		logger.Error("创建 Bot 失败", logger.ErrorField(err))
		return nil, ErrInternalServer
	}

	s.publishBotEvent(ctx, "create", bot.ID, userID, map[string]string{
		"name":     name,
		"provider": modelProvider,
		"model":    modelName,
	})

	logger.Info("创建 Bot 成功", logger.StringField("id", bot.ID))
	return bot, nil
}

func (s *botServiceImpl) UpdateBot(ctx context.Context, userID, id, name, description, avatar, modelName, apiKey, baseURL, embeddingModel, systemPrompt string, config map[string]string, status string) (*botmodel.Bot, error) {
	logger.Info("更新 Bot 请求", logger.StringField("id", id))

	bot, err := s.repo.GetBot(ctx, id)
	if err != nil {
		logger.Error("获取 Bot 失败", logger.ErrorField(err))
		return nil, ErrInternalServer
	}
	if bot == nil {
		return nil, ErrBotNotFound
	}

	configChanged := false
	if name != "" {
		bot.Name = name
	}
	if description != "" {
		bot.Description = description
	}
	if avatar != "" {
		bot.Avatar = avatar
	}
	if modelName != "" {
		bot.Model = modelName
		configChanged = true
	}
	if apiKey != "" {
		bot.APIKey = apiKey
		configChanged = true
	}
	if baseURL != "" {
		bot.BaseURL = baseURL
		configChanged = true
	}
	if embeddingModel != "" {
		bot.EmbeddingModel = embeddingModel
	}
	if systemPrompt != "" {
		bot.SystemPrompt = systemPrompt
		configChanged = true
	}
	if config != nil {
		bot.Config = botmodel.JSONMap(config)
		configChanged = true
	}
	if status != "" {
		bot.Status = status
	}
	bot.UpdatedAt = time.Now()

	if err := s.repo.UpdateBot(ctx, bot); err != nil {
		logger.Error("更新 Bot 失败", logger.ErrorField(err))
		return nil, ErrInternalServer
	}

	if configChanged && s.agentManager != nil {
		s.agentManager.InvalidateAgent(bot.ID)
	}

	logger.Info("更新 Bot 成功", logger.StringField("id", id))
	return bot, nil
}

func (s *botServiceImpl) DeleteBot(ctx context.Context, id string) error {
	logger.Info("删除 Bot 请求", logger.StringField("id", id))

	if err := s.repo.DeleteBot(ctx, id); err != nil {
		logger.Error("删除 Bot 失败", logger.ErrorField(err))
		return ErrInternalServer
	}

	if s.agentManager != nil {
		s.agentManager.InvalidateAgent(id)
	}

	logger.Info("删除 Bot 成功", logger.StringField("id", id))
	return nil
}

func (s *botServiceImpl) GetBot(ctx context.Context, id string) (*botmodel.Bot, error) {
	bot, err := s.repo.GetBot(ctx, id)
	if err != nil {
		logger.Error("获取 Bot 失败", logger.ErrorField(err))
		return nil, ErrInternalServer
	}
	return bot, nil
}

func (s *botServiceImpl) ListBots(ctx context.Context, userID, botType, status string, page, pageSize int) ([]*botmodel.Bot, int64, error) {
	offset := (page - 1) * pageSize
	bots, err := s.repo.ListBots(ctx, userID, botType, status, offset, pageSize)
	if err != nil {
		logger.Error("获取 Bot 列表失败", logger.ErrorField(err))
		return nil, 0, ErrInternalServer
	}

	total, err := s.repo.CountBots(ctx, userID, botType, status)
	if err != nil {
		logger.Error("统计 Bot 失败", logger.ErrorField(err))
		return nil, 0, ErrInternalServer
	}

	return bots, total, nil
}

func (s *botServiceImpl) CreateConversation(ctx context.Context, botID, userID, title string) (*botmodel.Conversation, error) {
	logger.Info("创建对话请求", logger.StringField("botID", botID), logger.StringField("userID", userID))

	conversation := &botmodel.Conversation{
		BotID:     botID,
		UserID:    userID,
		Title:     title,
		Status:    "active",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := s.repo.CreateConversation(ctx, conversation); err != nil {
		logger.Error("创建对话失败", logger.ErrorField(err))
		return nil, ErrInternalServer
	}

	logger.Info("创建对话成功", logger.StringField("id", conversation.ID))
	return conversation, nil
}

func (s *botServiceImpl) UpdateConversation(ctx context.Context, id, title string) (*botmodel.Conversation, error) {
	logger.Info("更新对话请求", logger.StringField("id", id))

	conversation, err := s.repo.GetConversation(ctx, id)
	if err != nil {
		logger.Error("获取对话失败", logger.ErrorField(err))
		return nil, ErrInternalServer
	}
	if conversation == nil {
		return nil, ErrConversationNotFound
	}

	if title != "" {
		conversation.Title = title
	}
	conversation.UpdatedAt = time.Now()

	if err := s.repo.UpdateConversation(ctx, conversation); err != nil {
		logger.Error("更新对话失败", logger.ErrorField(err))
		return nil, ErrInternalServer
	}

	logger.Info("更新对话成功", logger.StringField("id", id))
	return conversation, nil
}

func (s *botServiceImpl) DeleteConversation(ctx context.Context, id string) error {
	logger.Info("删除对话请求", logger.StringField("id", id))

	if err := s.repo.DeleteConversation(ctx, id); err != nil {
		logger.Error("删除对话失败", logger.ErrorField(err))
		return ErrInternalServer
	}

	logger.Info("删除对话成功", logger.StringField("id", id))
	return nil
}

func (s *botServiceImpl) GetConversation(ctx context.Context, id string) (*botmodel.Conversation, error) {
	conversation, err := s.repo.GetConversation(ctx, id)
	if err != nil {
		logger.Error("获取对话失败", logger.ErrorField(err))
		return nil, ErrInternalServer
	}
	return conversation, nil
}

func (s *botServiceImpl) ListConversations(ctx context.Context, botID, userID string, page, pageSize int) ([]*botmodel.Conversation, int64, error) {
	offset := (page - 1) * pageSize
	conversations, err := s.repo.ListConversations(ctx, botID, userID, offset, pageSize)
	if err != nil {
		logger.Error("获取对话列表失败", logger.ErrorField(err))
		return nil, 0, ErrInternalServer
	}

	total, err := s.repo.CountConversations(ctx, botID, userID)
	if err != nil {
		logger.Error("统计对话失败", logger.ErrorField(err))
		return nil, 0, ErrInternalServer
	}

	return conversations, total, nil
}

func (s *botServiceImpl) SendMessage(ctx context.Context, userID, botID, conversationID, content string, stream bool, metadata map[string]string) (string, error) {
	logger.Info("发送消息请求", logger.StringField("userID", userID), logger.StringField("botID", botID))

	if s.agentManager == nil {
		return "", ErrAgentNotInitialized
	}

	bot, err := s.repo.GetBot(ctx, botID)
	if err != nil {
		logger.Error("获取 Bot 失败", logger.ErrorField(err))
		return "", ErrInternalServer
	}
	if bot == nil {
		return "", ErrBotNotFound
	}

	usePlatformModel := bot.Provider == "platform" || bot.APIKey == ""

	var conversation *botmodel.Conversation
	if conversationID == "" {
		conversation, err = s.CreateConversation(ctx, botID, userID, "")
		if err != nil {
			return "", err
		}
		conversationID = conversation.ID
	} else {
		conversation, err = s.repo.GetConversation(ctx, conversationID)
		if err != nil {
			return "", ErrInternalServer
		}
		if conversation == nil {
			return "", ErrConversationNotFound
		}
	}

	userMsg := &botmodel.Message{
		BotID:          botID,
		ConversationID: conversationID,
		Role:           "user",
		Content:        content,
		Metadata:       botmodel.JSONMap(metadata),
		CreatedAt:      time.Now(),
	}
	if err := s.repo.AddMessage(ctx, userMsg); err != nil {
		logger.Error("保存用户消息失败", logger.ErrorField(err))
		return "", ErrInternalServer
	}

	fullPrompt := content
	if s.vectorService != nil {
		fullPrompt = s.buildRAGPrompt(ctx, bot, content)
	}

	estimatedTokens := estimateTokenCount(fullPrompt)
	if usePlatformModel && s.billingService != nil {
		_, err = s.billingService.ConsumeModelCall(ctx, userID, bot.Provider, bot.Model, estimatedTokens, map[string]string{
			"bot_id": bot.ID,
		})
		if err != nil {
			if strings.Contains(err.Error(), "insufficient") {
				return "", ErrInsufficientBalance
			}
			logger.Error("计费调用失败", logger.ErrorField(err))
			return "", ErrInternalServer
		}
	}

	botAgent, err := s.getOrCreateAgent(bot)
	if err != nil {
		logger.Error("获取 Agent 失败", logger.ErrorField(err))
		return "", ErrInternalServer
	}

	historyMessages, err := s.buildHistoryMessages(ctx, conversationID, 20)
	if err != nil {
		logger.Warn("构建历史消息失败，使用简单对话", logger.ErrorField(err))
		response, err := botAgent.Chat(ctx, fullPrompt)
		if err != nil {
			logger.Error("Agent 调用失败", logger.ErrorField(err))
			return "", ErrInternalServer
		}
		s.saveAssistantMessage(ctx, botID, conversationID, response)
		return response, nil
	}

	currentMsg := schema.UserMessage(fullPrompt)
	allMessages := append(historyMessages, currentMsg)

	response, err := botAgent.ChatWithHistory(ctx, allMessages)
	if err != nil {
		logger.Error("Agent 调用失败", logger.ErrorField(err))
		return "", ErrInternalServer
	}

	s.saveAssistantMessage(ctx, botID, conversationID, response)

	s.publishBotEvent(ctx, "message", botID, userID, map[string]string{
		"conversation_id": conversationID,
		"tokens":          fmt.Sprintf("%d", estimatedTokens),
	})

	logger.Info("发送消息成功")
	return response, nil
}

func (s *botServiceImpl) SendMessageStream(ctx context.Context, userID, botID, conversationID, content string, metadata map[string]string, onChunk func(string) error) error {
	logger.Info("发送流式消息请求", logger.StringField("userID", userID), logger.StringField("botID", botID))

	if s.agentManager == nil {
		return ErrAgentNotInitialized
	}

	bot, err := s.repo.GetBot(ctx, botID)
	if err != nil {
		logger.Error("获取 Bot 失败", logger.ErrorField(err))
		return ErrInternalServer
	}
	if bot == nil {
		return ErrBotNotFound
	}

	usePlatformModel := bot.Provider == "platform" || bot.APIKey == ""

	if usePlatformModel && s.billingService != nil {
		estimatedTokens := estimateTokenCount(content)
		_, err = s.billingService.ConsumeModelCall(ctx, userID, bot.Provider, bot.Model, estimatedTokens, map[string]string{
			"bot_id": bot.ID,
		})
		if err != nil {
			if strings.Contains(err.Error(), "insufficient") {
				return ErrInsufficientBalance
			}
			logger.Error("计费调用失败", logger.ErrorField(err))
			return ErrInternalServer
		}
	}

	var conversation *botmodel.Conversation
	if conversationID == "" {
		conversation, err = s.CreateConversation(ctx, botID, userID, "")
		if err != nil {
			return err
		}
		conversationID = conversation.ID
	} else {
		conversation, err = s.repo.GetConversation(ctx, conversationID)
		if err != nil {
			return ErrInternalServer
		}
		if conversation == nil {
			return ErrConversationNotFound
		}
	}

	userMsg := &botmodel.Message{
		BotID:          botID,
		ConversationID: conversationID,
		Role:           "user",
		Content:        content,
		Metadata:       botmodel.JSONMap(metadata),
		CreatedAt:      time.Now(),
	}
	if err := s.repo.AddMessage(ctx, userMsg); err != nil {
		logger.Error("保存用户消息失败", logger.ErrorField(err))
		return ErrInternalServer
	}

	fullPrompt := content
	if s.vectorService != nil {
		fullPrompt = s.buildRAGPrompt(ctx, bot, content)
	}

	botAgent, err := s.getOrCreateAgent(bot)
	if err != nil {
		logger.Error("获取 Agent 失败", logger.ErrorField(err))
		return ErrInternalServer
	}

	historyMessages, err := s.buildHistoryMessages(ctx, conversationID, 20)
	if err != nil {
		logger.Warn("构建历史消息失败，使用简单对话", logger.ErrorField(err))
		var fullResponse string
		streamOnChunk := func(chunk string) error {
			fullResponse += chunk
			return onChunk(chunk)
		}
		if err := botAgent.ChatStream(ctx, fullPrompt, streamOnChunk); err != nil {
			logger.Error("Agent 流式调用失败", logger.ErrorField(err))
			return ErrInternalServer
		}
		s.saveAssistantMessage(ctx, botID, conversationID, fullResponse)
		return nil
	}

	currentMsg := schema.UserMessage(fullPrompt)
	allMessages := append(historyMessages, currentMsg)

	var fullResponse string
	streamOnChunk := func(chunk string) error {
		fullResponse += chunk
		return onChunk(chunk)
	}

	if err := botAgent.ChatStreamWithHistory(ctx, allMessages, streamOnChunk); err != nil {
		logger.Error("Agent 流式调用失败", logger.ErrorField(err))
		return ErrInternalServer
	}

	s.saveAssistantMessage(ctx, botID, conversationID, fullResponse)

	logger.Info("发送流式消息成功")
	return nil
}

func (s *botServiceImpl) GetHistory(ctx context.Context, botID, conversationID string, limit int, beforeTime *time.Time) ([]*botmodel.Message, bool, error) {
	messages, err := s.repo.GetMessages(ctx, conversationID, limit, beforeTime)
	if err != nil {
		logger.Error("获取对话历史失败", logger.ErrorField(err))
		return nil, false, ErrInternalServer
	}

	hasMore := len(messages) == limit
	return messages, hasMore, nil
}

func (s *botServiceImpl) CreatePrompt(ctx context.Context, userID, name, description, content, promptType string, isPreset, isPublic bool, config map[string]string) (*botmodel.Prompt, error) {
	logger.Info("创建提示词请求", logger.StringField("name", name), logger.StringField("userId", userID))

	prompt := &botmodel.Prompt{
		UserID:      userID,
		Name:        name,
		Description: description,
		Content:     content,
		Type:        promptType,
		IsPreset:    isPreset,
		IsPublic:    isPublic,
		Config:      botmodel.JSONMap(config),
	}

	if err := s.repo.CreatePrompt(ctx, prompt); err != nil {
		logger.Error("创建提示词失败", logger.ErrorField(err))
		return nil, ErrInternalServer
	}

	logger.Info("创建提示词成功", logger.StringField("id", prompt.ID))
	return prompt, nil
}

func (s *botServiceImpl) UpdatePrompt(ctx context.Context, id, name, description, content string, isPublic *bool, config map[string]string) (*botmodel.Prompt, error) {
	logger.Info("更新提示词请求", logger.StringField("id", id))

	prompt, err := s.repo.GetPrompt(ctx, id)
	if err != nil {
		logger.Error("获取提示词失败", logger.ErrorField(err))
		return nil, ErrInternalServer
	}
	if prompt == nil {
		return nil, ErrPromptNotFound
	}

	if name != "" {
		prompt.Name = name
	}
	if description != "" {
		prompt.Description = description
	}
	if content != "" {
		prompt.Content = content
	}
	if isPublic != nil {
		prompt.IsPublic = *isPublic
	}
	if config != nil {
		prompt.Config = botmodel.JSONMap(config)
	}
	prompt.UpdatedAt = time.Now()

	if err := s.repo.UpdatePrompt(ctx, prompt); err != nil {
		logger.Error("更新提示词失败", logger.ErrorField(err))
		return nil, ErrInternalServer
	}

	logger.Info("更新提示词成功", logger.StringField("id", id))
	return prompt, nil
}

func (s *botServiceImpl) DeletePrompt(ctx context.Context, id string) error {
	logger.Info("删除提示词请求", logger.StringField("id", id))

	if err := s.repo.DeletePrompt(ctx, id); err != nil {
		logger.Error("删除提示词失败", logger.ErrorField(err))
		return ErrInternalServer
	}

	logger.Info("删除提示词成功", logger.StringField("id", id))
	return nil
}

func (s *botServiceImpl) GetPrompt(ctx context.Context, id string) (*botmodel.Prompt, error) {
	prompt, err := s.repo.GetPrompt(ctx, id)
	if err != nil {
		logger.Error("获取提示词失败", logger.ErrorField(err))
		return nil, ErrInternalServer
	}
	return prompt, nil
}

func (s *botServiceImpl) ListPrompts(ctx context.Context, userID, promptType string, isPreset, isPublic *bool, page, pageSize int) ([]*botmodel.Prompt, int64, error) {
	offset := (page - 1) * pageSize
	prompts, err := s.repo.ListPrompts(ctx, userID, promptType, isPreset, isPublic, offset, pageSize)
	if err != nil {
		logger.Error("获取提示词列表失败", logger.ErrorField(err))
		return nil, 0, ErrInternalServer
	}

	total, err := s.repo.CountPrompts(ctx, userID, promptType, isPreset, isPublic)
	if err != nil {
		logger.Error("统计提示词失败", logger.ErrorField(err))
		return nil, 0, ErrInternalServer
	}

	return prompts, total, nil
}

func (s *botServiceImpl) GetUserMemory(ctx context.Context, userID, botID string) ([]*botmodel.UserMemory, error) {
	return s.repo.GetUserMemory(ctx, userID, botID)
}

func (s *botServiceImpl) SetUserMemory(ctx context.Context, userID, botID, key, value string) error {
	memory := &botmodel.UserMemory{
		UserID: userID,
		BotID:  botID,
		Key:    key,
		Value:  value,
	}
	return s.repo.SetUserMemory(ctx, memory)
}

func (s *botServiceImpl) WithTransaction(ctx context.Context, fn func(txService BotService) error) error {
	return s.repo.WithTransaction(ctx, func(txRepo dao.BotRepository) error {
		txService := NewBotService(txRepo, s.agentManager, s.einoManager, s.billingService, s.vectorService, s.producer)
		return fn(txService)
	})
}

func (s *botServiceImpl) getOrCreateAgent(bot *botmodel.Bot) (agent.BotAgent, error) {
	cfg := &agent.AgentConfig{
		ID:           bot.ID,
		Name:         bot.Name,
		Description:  bot.Description,
		SystemPrompt: bot.SystemPrompt,
	}

	if bot.Provider != "" && bot.Provider != "platform" && bot.APIKey != "" {
		chatModel, err := s.createChatModelForBot(bot)
		if err != nil {
			logger.Warn("为 Bot 创建自定义 ChatModel 失败，使用全局模型",
				logger.StringField("botID", bot.ID),
				logger.ErrorField(err))
		} else if chatModel != nil {
			cfg.ChatModel = chatModel
		}
	}

	return s.agentManager.GetOrCreateAgent(cfg)
}

func (s *botServiceImpl) createChatModelForBot(bot *botmodel.Bot) (model.BaseChatModel, error) {
	registry := provider.GetProviderRegistry()
	p, err := registry.GetProvider(bot.Provider)
	if err != nil {
		return nil, err
	}

	return p.NewChatModel(bot.APIKey, bot.BaseURL, bot.Model)
}

func (s *botServiceImpl) buildRAGPrompt(ctx context.Context, bot *botmodel.Bot, content string) string {
	collectionID := "documents"
	if configVal, ok := bot.Config["collection_id"]; ok {
		collectionID = fmt.Sprintf("%v", configVal)
	}

	results, err := s.vectorService.TextSearch(ctx, collectionID, content, 5)
	if err != nil {
		logger.Warn("RAG 查询失败，继续普通对话", logger.ErrorField(err))
		return content
	}
	if len(results) == 0 {
		return content
	}

	var ragContext strings.Builder
	ragContext.WriteString("以下是一些参考文档内容，请基于这些信息回答用户问题：\n\n")
	for i, result := range results {
		ragContext.WriteString(fmt.Sprintf("[文档 %d]:\n%s\n\n", i+1, result))
	}
	ragContext.WriteString("用户的问题：\n" + content + "\n\n请基于上述参考内容回答，仅使用参考内容中相关的信息。")

	logger.Info("RAG 查询成功，找到相关文档", logger.IntField("count", len(results)))
	return ragContext.String()
}

func (s *botServiceImpl) buildHistoryMessages(ctx context.Context, conversationID string, limit int) ([]*schema.Message, error) {
	messages, err := s.repo.GetMessages(ctx, conversationID, limit, nil)
	if err != nil {
		return nil, err
	}

	var schemaMessages []*schema.Message
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		switch msg.Role {
		case "user":
			schemaMessages = append(schemaMessages, schema.UserMessage(msg.Content))
		case "assistant":
			schemaMessages = append(schemaMessages, schema.AssistantMessage(msg.Content, nil))
		}
	}

	return schemaMessages, nil
}

func (s *botServiceImpl) saveAssistantMessage(ctx context.Context, botID, conversationID, content string) {
	assistantMsg := &botmodel.Message{
		BotID:          botID,
		ConversationID: conversationID,
		Role:           "assistant",
		Content:        content,
		CreatedAt:      time.Now(),
	}
	if err := s.repo.AddMessage(ctx, assistantMsg); err != nil {
		logger.Warn("保存助理消息失败", logger.ErrorField(err))
	}
}

func (s *botServiceImpl) publishBotEvent(ctx context.Context, action, botID, userID string, metadata map[string]string) {
	if s.producer == nil {
		return
	}

	event := map[string]interface{}{
		"action":    action,
		"bot_id":    botID,
		"user_id":   userID,
		"metadata":  metadata,
		"timestamp": time.Now().Unix(),
	}

	eventJSON, err := json.Marshal(event)
	if err != nil {
		logger.Warn("序列化 Bot 事件失败", logger.ErrorField(err))
		return
	}

	if err := s.producer.Send(ctx, "bot_events", botID, eventJSON); err != nil {
		logger.Warn("发送 Bot 事件失败",
			logger.StringField("action", action),
			logger.StringField("botID", botID),
			logger.ErrorField(err))
	}
}

func estimateTokenCount(text string) int {
	charCount := utf8.RuneCountInString(text)
	asciiCount := len(text) - charCount*3
	if asciiCount < 0 {
		asciiCount = 0
	}
	nonAsciiCount := charCount - asciiCount
	return nonAsciiCount*2 + asciiCount/4 + 50
}
