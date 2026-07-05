package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"Logos/config"
	"Logos/internal/bot/agent"
	"Logos/internal/bot/coordinator"
	botmemory "Logos/internal/bot/memory"
	"Logos/internal/bot/provider"
	botTools "Logos/internal/bot/tools"
	"Logos/internal/mcp"
	"Logos/internal/mcp_server"
	"Logos/internal/service/ai/bot/dao"
	botmodel "Logos/internal/service/ai/bot/model"
	"Logos/pkg/cache"
	"Logos/pkg/client"
	"Logos/pkg/eino"
	"Logos/pkg/logger"
	"Logos/pkg/outbox"
	"Logos/pkg/strutil"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
)

type VectorService interface {
	TextSearch(ctx context.Context, collectionID string, text string, topK int) ([]string, error)
	SearchWithScores(ctx context.Context, collectionID string, text string, topK int) ([]*VectorSearchResult, error)
	ListCollections(ctx context.Context) ([]*VectorCollectionInfo, error)
	UpdateCollectionEmbedding(ctx context.Context, collectionID, model, baseURL, apiKey string) error
	Vectorize(ctx context.Context, text string, collectionID string, metadata map[string]string) (string, error)
}

type VectorSearchResult struct {
	ID       string
	Content  string
	Score    float64
	Metadata map[string]string
}

type VectorCollectionInfo struct {
	ID   string
	Name string
	Size int64
}

type SearchService interface {
	Search(ctx context.Context, query string, topK int) ([]*SearchResultItem, error)
}

type SearchResultItem struct {
	ID      string
	Title   string
	Content string
	Score   float64
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
	ErrQQNumberDuplicate    = errors.New("该QQ号已被其他Bot绑定")
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

	SendMessage(ctx context.Context, userID, botID, conversationID, content string, stream bool, metadata map[string]string) (string, string, float64, int, error) // 返回 content, conversationID, cost, tokens, error
	SendMessageStream(ctx context.Context, userID, botID, conversationID, content string, metadata map[string]string, onChunk func(string) error) (string, error)  // 返回 conversationID
	GetHistory(ctx context.Context, botID, conversationID string, limit int, beforeTime *time.Time) ([]*botmodel.Message, bool, error)

	CreatePrompt(ctx context.Context, userID, name, description, content, promptType string, isPreset, isPublic bool, config map[string]string) (*botmodel.Prompt, error)
	UpdatePrompt(ctx context.Context, id, name, description, content string, isPublic *bool, config map[string]string) (*botmodel.Prompt, error)
	DeletePrompt(ctx context.Context, id string) error
	GetPrompt(ctx context.Context, id string) (*botmodel.Prompt, error)
	ListPrompts(ctx context.Context, userID, promptType string, isPreset, isPublic *bool, page, pageSize int) ([]*botmodel.Prompt, int64, error)

	GetUserMemory(ctx context.Context, userID, botID string) ([]*botmodel.UserMemory, error)
	SetUserMemory(ctx context.Context, userID, botID, key, value string) error
	SetUserMemoryWithCategory(ctx context.Context, userID, botID, key, value, category string) error
	DeleteUserMemory(ctx context.Context, userID, botID, key string) error
	DeleteAllUserMemories(ctx context.Context, userID, botID string) error

	ChatWithCoordinator(ctx context.Context, userID, content string) (string, error)
	ChatWithCoordinatorStream(ctx context.Context, userID, content string, onChunk func(string) error) error

	WithTransaction(ctx context.Context, fn func(txService BotService) error) error
}

type botServiceImpl struct {
	repo             dao.BotRepository
	agentManager     *agent.AgentManager
	einoManager      *eino.EinoManager
	billingService   BillingService
	vectorService    VectorService
	searchService    SearchService
	outboxRepo       outbox.OutboxRepository
	mcpRegistry      *mcp_server.ToolRegistry
	mcpClientMgr     *mcp.MCPClientManager
	mcpClient        *client.MCPClient
	cfg              *config.Config
	knowledgeService botTools.GraphWriteService
	graphSearchSvc   botTools.GraphService
	kbCache          sync.Map
}

func NewBotService(
	repo dao.BotRepository,
	agentManager *agent.AgentManager,
	einoManager *eino.EinoManager,
	billingService BillingService,
	vectorService VectorService,
	searchService SearchService,
	outboxRepo outbox.OutboxRepository,
	mcpClient *client.MCPClient,
	cfg *config.Config,
	knowledgeService botTools.GraphWriteService,
	graphSearchSvc botTools.GraphService,
) BotService {
	mcpReg := mcp_server.DefaultRegistry()
	mcpClientMgr := mcp.NewMCPClientManager()

	var coord *coordinator.Coordinator
	if knowledgeService != nil && graphSearchSvc != nil {
		coord = coordinator.InitCoordinatorWithGraph(einoManager, agentManager, mcpReg, nil, knowledgeService, graphSearchSvc)
	} else {
		coord = coordinator.InitCoordinator(einoManager, agentManager, mcpReg)
	}
	if coord != nil {
		logger.Info("Coordinator 已初始化")
	}

	return &botServiceImpl{
		repo:             repo,
		agentManager:     agentManager,
		einoManager:      einoManager,
		billingService:   billingService,
		vectorService:    vectorService,
		searchService:    searchService,
		outboxRepo:       outboxRepo,
		mcpRegistry:      mcpReg,
		mcpClientMgr:     mcpClientMgr,
		mcpClient:        mcpClient,
		cfg:              cfg,
		knowledgeService: knowledgeService,
		graphSearchSvc:   graphSearchSvc,
	}
}

func (s *botServiceImpl) CreateBot(ctx context.Context, userID, name, description, avatar, botType, modelProvider, modelName, apiKey, baseURL, embeddingModel, systemPrompt string, config map[string]string) (*botmodel.Bot, error) {
	logger.Info("创建 Bot 请求", logger.StringField("name", name), logger.StringField("userID", userID), logger.StringField("avatar", avatar))

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
		QQNumber:       config["qq_number"],
		Status:         "active",
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	// Validate QQ number uniqueness
	if config != nil {
		if qqNum, ok := config["qq_number"]; ok && qqNum != "" {
			existing, err := s.repo.GetBotByQQNumber(ctx, qqNum)
			if err != nil {
				logger.Error("检查QQ号唯一性失败", logger.ErrorField(err))
				return nil, ErrInternalServer
			}
			if existing != nil {
				return nil, ErrQQNumberDuplicate
			}
		}
	}

	err := s.repo.WithTransaction(ctx, func(txRepo dao.BotRepository) error {
		if err := txRepo.CreateBot(ctx, bot); err != nil {
			return err
		}
		event := map[string]any{
			"action":    "create",
			"bot_id":    bot.ID,
			"user_id":   userID,
			"metadata":  map[string]string{"name": name, "provider": modelProvider, "model": modelName},
			"timestamp": time.Now().Unix(),
		}
		return s.outboxRepo.SaveWithTx(ctx, txRepo.DB(), "bot_events", bot.ID, event)
	})
	if err != nil {
		logger.Error("创建 Bot 失败", logger.ErrorField(err))
		return nil, ErrInternalServer
	}

	s.syncEmbeddingToCollections(ctx, bot)

	// 写入 QQ-Bot 绑定缓存
	if bot.QQNumber != "" {
		s.updateQQBotCache(bot.QQNumber, bot.ID)
	}

	logger.Info("创建 Bot 成功", logger.StringField("id", bot.ID))
	return bot, nil
}

func (s *botServiceImpl) UpdateBot(ctx context.Context, userID, id, name, description, avatar, modelName, apiKey, baseURL, embeddingModel, systemPrompt string, config map[string]string, status string) (*botmodel.Bot, error) {
	logger.Info("更新 Bot 请求",
		logger.StringField("id", id),
		logger.StringField("userID", userID),
		logger.StringField("name", name),
		logger.StringField("systemPrompt", systemPrompt),
		logger.StringField("avatar", avatar))

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
		if qqNum, ok := config["qq_number"]; ok && qqNum != "" {
			existing, err := s.repo.GetBotByQQNumber(ctx, qqNum)
			if err != nil {
				logger.Error("检查QQ号唯一性失败", logger.ErrorField(err))
				return nil, ErrInternalServer
			}
			if existing != nil && existing.ID != id {
				return nil, ErrQQNumberDuplicate
			}
			bot.QQNumber = qqNum
		}
	}
	if status != "" {
		bot.Status = status
	}
	bot.UpdatedAt = time.Now()

	if err := s.repo.UpdateBot(ctx, bot); err != nil {
		logger.Error("更新 Bot 失败", logger.ErrorField(err))
		return nil, ErrInternalServer
	}

	s.syncEmbeddingToCollections(ctx, bot)

	if configChanged && s.agentManager != nil {
		s.agentManager.InvalidateAgent(bot.ID)
	}

	// 更新 QQ-Bot 绑定缓存
	if bot.QQNumber != "" {
		s.updateQQBotCache(bot.QQNumber, bot.ID)
	}

	logger.Info("更新 Bot 成功", logger.StringField("id", id))
	return bot, nil
}

func (s *botServiceImpl) syncEmbeddingToCollections(ctx context.Context, bot *botmodel.Bot) {
	if s.vectorService == nil {
		return
	}

	enableRag := false
	if configVal, ok := bot.Config["enable_rag"]; ok {
		enableRag = configVal == "true"
	}
	if !enableRag {
		return
	}

	if bot.EmbeddingModel == "" {
		return
	}

	collectionIDsStr, ok := bot.Config["collection_ids"]
	if !ok || collectionIDsStr == "" {
		return
	}

	collectionIDs := strings.Split(collectionIDsStr, ",")
	embeddingBaseURL, _ := bot.Config["embedding_base_url"]
	embeddingAPIKey, _ := bot.Config["embedding_api_key"]

	for _, cid := range collectionIDs {
		cid = strings.TrimSpace(cid)
		if cid == "" {
			continue
		}
		if err := s.vectorService.UpdateCollectionEmbedding(ctx, cid, bot.EmbeddingModel, embeddingBaseURL, embeddingAPIKey); err != nil {
			logger.Error("同步Embedding配置到集合失败",
				logger.StringField("collectionID", cid),
				logger.ErrorField(err))
		} else {
			logger.Info("同步Embedding配置到集合成功",
				logger.StringField("collectionID", cid),
				logger.StringField("embeddingModel", bot.EmbeddingModel))
		}
	}
}

func (s *botServiceImpl) autoSaveToKnowledgeBase(bot *botmodel.Bot, userContent, assistantResponse string, forceAutoSave bool) {
	if s.vectorService == nil {
		return
	}

	autoSave := forceAutoSave
	if !autoSave {
		if configVal, ok := bot.Config["auto_save_to_kb"]; ok {
			autoSave = configVal == "true"
		}
	}
	if !autoSave {
		return
	}

	collectionIDsStr, ok := bot.Config["collection_ids"]
	if !ok || collectionIDsStr == "" {
		return
	}

	collectionIDs := strings.Split(collectionIDsStr, ",")
	text := fmt.Sprintf("用户: %s\n助手: %s", userContent, assistantResponse)

	for _, cid := range collectionIDs {
		cid = strings.TrimSpace(cid)
		if cid == "" {
			continue
		}
		vectorCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		vectorID, err := s.vectorService.Vectorize(vectorCtx, text, cid, map[string]string{
			"source":   "bot_auto_save",
			"bot_id":   bot.ID,
			"bot_name": bot.Name,
		})
		cancel()
		if err != nil {
			logger.Error("自动存入知识库失败",
				logger.StringField("collectionID", cid),
				logger.ErrorField(err))
		} else {
			logger.Info("自动存入知识库成功",
				logger.StringField("collectionID", cid),
				logger.StringField("vectorID", vectorID))
		}
	}
}

func (s *botServiceImpl) DeleteBot(ctx context.Context, id string) error {
	logger.Info("删除 Bot 请求", logger.StringField("id", id))

	// 先获取 Bot，如果有 QQ 号则清除绑定，避免唯一索引冲突
	bot, err := s.repo.GetBot(ctx, id)
	if err != nil {
		logger.Error("获取 Bot 失败", logger.ErrorField(err))
		return ErrInternalServer
	}
	if bot != nil && bot.QQNumber != "" {
		bot.QQNumber = ""
		if err := s.repo.UpdateBot(ctx, bot); err != nil {
			logger.Error("清除 Bot QQ 绑定失败", logger.ErrorField(err))
			return ErrInternalServer
		}
	}

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

	id := uuid.NewString()
	conversation := &botmodel.Conversation{
		ID:        id,
		ChatID:    id,
		ChatType:  1,
		Name:      "",
		Avatar:    "",
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

func (s *botServiceImpl) createConversationWithID(ctx context.Context, id, botID, userID, title string) (*botmodel.Conversation, error) {
	logger.Info("创建对话请求(指定ID)", logger.StringField("id", id), logger.StringField("botID", botID), logger.StringField("userID", userID))

	conversation := &botmodel.Conversation{
		ID:        id,
		ChatID:    id,
		ChatType:  1,
		Name:      "",
		Avatar:    "",
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

func (s *botServiceImpl) SendMessage(ctx context.Context, userID, botID, conversationID, content string, stream bool, metadata map[string]string) (string, string, float64, int, error) {
	startTime := time.Now()
	defaultConversationID := fmt.Sprintf("%s_%s", userID, botID)

	if conversationID == "" || len(conversationID) > 64 {
		conversationID = defaultConversationID
	}

	logger.Info("发送消息请求", logger.StringField("userID", userID), logger.StringField("botID", botID), logger.StringField("conversationID", conversationID))

	if s.agentManager == nil {
		logger.Error("AgentManager 未初始化")
		return "", "", 0, 0, ErrAgentNotInitialized
	}

	bot, err := s.repo.GetBot(ctx, botID)
	if err != nil {
		logger.Error("获取 Bot 失败", logger.ErrorField(err))
		return "", "", 0, 0, ErrInternalServer
	}
	if bot == nil {
		logger.Error("Bot 不存在", logger.StringField("botID", botID))
		return "", "", 0, 0, ErrBotNotFound
	}

	logger.Info("Bot 配置",
		logger.StringField("botID", botID),
		logger.StringField("provider", bot.Provider),
		logger.StringField("model", bot.Model),
		logger.BoolField("hasAPIKey", bot.APIKey != ""),
		logger.StringField("baseURL", bot.BaseURL),
		logger.StringField("elapsed_get_bot", time.Since(startTime).String()))

	usePlatformModel := bot.Provider == "platform" || bot.Provider == "" || bot.APIKey == ""

	if metadata != nil {
		if apiKey, ok := metadata["api_key"]; ok && apiKey != "" {
			overrideBot := *bot
			overrideBot.APIKey = apiKey
			if provider, ok := metadata["provider"]; ok && provider != "" {
				overrideBot.Provider = provider
			}
			if model, ok := metadata["model"]; ok && model != "" {
				overrideBot.Model = model
			}
			if baseURL, ok := metadata["base_url"]; ok && baseURL != "" {
				overrideBot.BaseURL = baseURL
			}
			bot = &overrideBot
			usePlatformModel = false
			logger.Info("使用metadata中的AI配置",
				logger.StringField("provider", bot.Provider),
				logger.StringField("model", bot.Model),
				logger.StringField("baseURL", bot.BaseURL))
		}
	}
	_ = usePlatformModel // 始终计费，不再区分

	var conversation *botmodel.Conversation
	conversation, err = s.repo.GetConversation(ctx, conversationID)
	if err != nil || conversation == nil {
		logger.Warn("对话不存在，创建新对话", logger.StringField("conversation_id", conversationID), logger.ErrorField(err))
		conversation, err = s.createConversationWithID(ctx, conversationID, botID, userID, "")
		if err != nil {
			return "", "", 0, 0, err
		}
	}

	userMsg := &botmodel.Message{
		ID:             uuid.NewString(),
		SenderID:       userID,
		BotID:          botID,
		ConversationID: conversationID,
		ChatID:         conversationID,
		Role:           "user",
		Content:        strutil.CleanInvalidUTF8(content),
		Metadata:       botmodel.JSONMap(metadata),
		MentionUserIDs: botmodel.StringSlice{},
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	if err = s.repo.AddMessage(ctx, userMsg); err != nil {
		logger.Error("保存用户消息失败", logger.ErrorField(err))
		return "", "", 0, 0, ErrInternalServer
	}

	estimatedTokens := estimateTokenCount(content)
	var cost float64

	// 计费移到 AI 调用之后，先继续处理

	agentStartTime := time.Now()
	botAgent, err := s.getOrCreateAgentWithMemory(ctx, bot, userID)
	if err != nil {
		logger.Error("获取 Agent 失败", logger.ErrorField(err))
		return "", "", 0, 0, fmt.Errorf("Agent 创建失败，请检查 Bot 的 API 配置: %w", err)
	}
	logger.Info("Agent 准备完成", logger.StringField("botID", botID), logger.StringField("elapsed_agent", time.Since(agentStartTime).String()))

	historyMessages, err := s.buildHistoryMessages(ctx, conversationID, 20)
	if err != nil {
		logger.Warn("构建历史消息失败，使用简单对话", logger.ErrorField(err))
		aiTimeout := 90 * time.Second
		if deadline, ok := ctx.Deadline(); ok {
			remaining := time.Until(deadline)
			if remaining < 15*time.Second {
				return "", "", 0, 0, fmt.Errorf("剩余时间不足，无法调用 AI: %v", remaining)
			}
			aiTimeout = remaining - 10*time.Second
		}
		aiCtx, aiCancel := context.WithTimeout(ctx, aiTimeout)
		defer aiCancel()

		chatResp, chatErr := botAgent.Chat(aiCtx, content)
		if chatErr != nil {
			logger.Error("Agent 调用失败", logger.ErrorField(chatErr), logger.StringField("elapsed_ai", time.Since(agentStartTime).String()))
			return "", "", 0, 0, fmt.Errorf("AI 调用失败: %w", chatErr)
		}

		// AI 调用完成后计费
		inputTokens := estimateTokenCount(content)
		outputTokens := estimateTokenCount(chatResp)
		estimatedTokens = inputTokens + outputTokens
		if s.billingService != nil {
			cost = s.doBilling(ctx, userID, bot, inputTokens, outputTokens)
		}

		s.saveAssistantMessage(ctx, botID, conversationID, chatResp)
		logger.Info("发送消息成功(简单对话)", logger.StringField("elapsed_total", time.Since(startTime).String()))
		return chatResp, conversationID, cost, estimatedTokens, nil
	}

	allMessages := historyMessages

	aiTimeout := 90 * time.Second
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining < 15*time.Second {
			return "", "", 0, 0, fmt.Errorf("剩余时间不足，无法调用 AI: %v", remaining)
		}
		aiTimeout = remaining - 10*time.Second
	}
	aiCtx, aiCancel := context.WithTimeout(ctx, aiTimeout)
	defer aiCancel()

	logger.Info("开始 AI 调用", logger.StringField("botID", botID), logger.StringField("timeout", aiTimeout.String()))
	aiCallStart := time.Now()

	response, err := botAgent.ChatWithHistory(aiCtx, allMessages)
	if err != nil {
		logger.Error("Agent 调用失败", logger.ErrorField(err), logger.StringField("elapsed_ai", time.Since(aiCallStart).String()))
		return "", "", 0, 0, fmt.Errorf("AI 调用失败: %w", err)
	}

	logger.Info("AI 调用完成", logger.StringField("botID", botID), logger.StringField("elapsed_ai", time.Since(aiCallStart).String()))

	// AI 调用完成后计费：估算输入+输出 tokens
	inputTokens := estimateTokenCount(content)
	outputTokens := estimateTokenCount(response)
	totalTokens := inputTokens + outputTokens
	estimatedTokens = totalTokens

	if s.billingService != nil {
		cost = s.doBilling(ctx, userID, bot, inputTokens, outputTokens)
	}

	s.saveAssistantMessage(ctx, botID, conversationID, response)

	autoSave := metadata != nil && metadata["auto_save_to_kb"] == "true"
	go s.autoSaveToKnowledgeBase(bot, content, response, autoSave)

	event := map[string]any{
		"action":    "message",
		"bot_id":    botID,
		"user_id":   userID,
		"metadata":  map[string]string{"conversation_id": conversationID, "tokens": fmt.Sprintf("%d", estimatedTokens)},
		"timestamp": time.Now().Unix(),
	}
	if err := s.outboxRepo.Save(ctx, s.repo.DB(), "bot_events", botID, event); err != nil {
		logger.Warn("保存 Bot 事件到 outbox 失败",
			logger.StringField("action", "message"),
			logger.StringField("botID", botID),
			logger.ErrorField(err))
	}

	logger.Info("发送消息成功", logger.StringField("elapsed_total", time.Since(startTime).String()))
	return response, conversationID, cost, estimatedTokens, nil
}

func (s *botServiceImpl) SendMessageStream(ctx context.Context, userID, botID, conversationID, content string, metadata map[string]string, onChunk func(string) error) (string, error) {
	// 使用固定的 conversation ID: {userID}_{botID}
	defaultConversationID := fmt.Sprintf("%s_%s", userID, botID)

	// 如果前端没有传有效的 conversationID，使用默认的
	if conversationID == "" || len(conversationID) > 64 {
		conversationID = defaultConversationID
	}

	logger.Info("发送流式消息请求", logger.StringField("userID", userID), logger.StringField("botID", botID), logger.StringField("conversationID", conversationID))

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
	_ = usePlatformModel // 始终计费，不再区分

	var conversation *botmodel.Conversation
	conversation, err = s.repo.GetConversation(ctx, conversationID)
	if err != nil || conversation == nil {
		logger.Warn("对话不存在，创建新对话", logger.StringField("conversation_id", conversationID), logger.ErrorField(err))
		conversation, err = s.createConversationWithID(ctx, conversationID, botID, userID, "")
		if err != nil {
			return "", err
		}
	}

	userMsg := &botmodel.Message{
		ID:             uuid.NewString(),
		SenderID:       userID,
		BotID:          botID,
		ConversationID: conversationID,
		ChatID:         conversationID,
		Role:           "user",
		Content:        content,
		Metadata:       botmodel.JSONMap(metadata),
		MentionUserIDs: botmodel.StringSlice{},
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	if err = s.repo.AddMessage(ctx, userMsg); err != nil {
		logger.Error("保存用户消息失败", logger.ErrorField(err))
		return "", ErrInternalServer
	}

	botAgent, err := s.getOrCreateAgentWithMemory(ctx, bot, userID)
	if err != nil {
		logger.Error("获取 Agent 失败", logger.ErrorField(err))
		return "", ErrInternalServer
	}

	historyMessages, err := s.buildHistoryMessages(ctx, conversationID, 20)
	if err != nil {
		logger.Warn("构建历史消息失败，使用简单对话", logger.ErrorField(err))
		var fullResponse strings.Builder
		streamOnChunk := func(chunk string) error {
			fullResponse.WriteString(chunk)
			return onChunk(chunk)
		}
		if err := botAgent.ChatStream(ctx, content, streamOnChunk); err != nil {
			logger.Error("Agent 流式调用失败", logger.ErrorField(err))
			return "", ErrInternalServer
		}

		// 简单流式对话完成后计费
		if s.billingService != nil {
			inputTokens := estimateTokenCount(content)
			outputTokens := estimateTokenCount(fullResponse.String())
			s.doBilling(ctx, userID, bot, inputTokens, outputTokens)
		}

		s.saveAssistantMessage(ctx, botID, conversationID, fullResponse.String())
		return conversationID, nil
	}

	allMessages := historyMessages

	var fullResponse strings.Builder
	streamOnChunk := func(chunk string) error {
		fullResponse.WriteString(chunk)
		return onChunk(chunk)
	}

	if err := botAgent.ChatStreamWithHistory(ctx, allMessages, streamOnChunk); err != nil {
		logger.Error("Agent 流式调用失败", logger.ErrorField(err))
		return "", ErrInternalServer
	}

	// 流式调用完成后计费
	if s.billingService != nil {
		inputTokens := estimateTokenCount(content)
		outputTokens := estimateTokenCount(fullResponse.String())
		s.doBilling(ctx, userID, bot, inputTokens, outputTokens)
	}

	s.saveAssistantMessage(ctx, botID, conversationID, fullResponse.String())

	autoSave := metadata != nil && metadata["auto_save_to_kb"] == "true"
	go s.autoSaveToKnowledgeBase(bot, content, fullResponse.String(), autoSave)

	event := map[string]any{
		"action":    "message_stream",
		"bot_id":    botID,
		"user_id":   userID,
		"metadata":  map[string]string{"conversation_id": conversationID},
		"timestamp": time.Now().Unix(),
	}
	if err := s.outboxRepo.Save(ctx, s.repo.DB(), "bot_events", botID, event); err != nil {
		logger.Warn("保存 Bot 事件到 outbox 失败",
			logger.StringField("action", "message_stream"),
			logger.StringField("botID", botID),
			logger.ErrorField(err))
	}

	logger.Info("发送流式消息成功")
	return conversationID, nil
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
	return s.SetUserMemoryWithCategory(ctx, userID, botID, key, value, "fact")
}

func (s *botServiceImpl) SetUserMemoryWithCategory(ctx context.Context, userID, botID, key, value, category string) error {
	existing, err := s.repo.GetUserMemoryByKey(ctx, userID, botID, key)
	if err == nil && existing != nil {
		existing.Value = value
		if category != "" {
			existing.Category = category
		}
		return s.repo.SetUserMemory(ctx, existing)
	}
	memory := &botmodel.UserMemory{
		UserID:     userID,
		BotID:      botID,
		Key:        key,
		Value:      value,
		Category:   category,
		Source:     "manual",
		Confidence: 1.0,
	}
	return s.repo.SetUserMemory(ctx, memory)
}

func (s *botServiceImpl) DeleteUserMemory(ctx context.Context, userID, botID, key string) error {
	return s.repo.DeleteUserMemory(ctx, userID, botID, key)
}

func (s *botServiceImpl) DeleteAllUserMemories(ctx context.Context, userID, botID string) error {
	memories, err := s.repo.GetUserMemoriesByUser(ctx, userID, botID)
	if err != nil {
		return err
	}
	for _, mem := range memories {
		_ = s.repo.DeleteUserMemoryByID(ctx, mem.ID)
	}
	return nil
}

func (s *botServiceImpl) ChatWithCoordinator(ctx context.Context, userID, content string) (string, error) {
	logger.Info("Coordinator 协作请求", logger.StringField("userID", userID))

	coord := coordinator.GetCoordinator()
	if coord == nil {
		return "", ErrAgentNotInitialized
	}

	response, err := coord.Chat(ctx, content)
	if err != nil {
		logger.Error("Coordinator 调用失败", logger.ErrorField(err))
		return "", ErrInternalServer
	}

	// AI 调用完成后计费
	if s.billingService != nil {
		inputTokens := estimateTokenCount(content)
		outputTokens := estimateTokenCount(response)
		s.doBilling(ctx, userID, &botmodel.Bot{Provider: "platform", Model: "coordinator"}, inputTokens, outputTokens)
	}

	return response, nil
}

func (s *botServiceImpl) ChatWithCoordinatorStream(ctx context.Context, userID, content string, onChunk func(string) error) error {
	logger.Info("Coordinator 流式协作请求", logger.StringField("userID", userID))

	coord := coordinator.GetCoordinator()
	if coord == nil {
		return ErrAgentNotInitialized
	}

	var fullResponse strings.Builder
	wrappedOnChunk := func(chunk string) error {
		fullResponse.WriteString(chunk)
		return onChunk(chunk)
	}

	if err := coord.ChatStream(ctx, content, wrappedOnChunk); err != nil {
		logger.Error("Coordinator 流式调用失败", logger.ErrorField(err))
		return ErrInternalServer
	}

	// 流式调用完成后计费
	if s.billingService != nil {
		inputTokens := estimateTokenCount(content)
		outputTokens := estimateTokenCount(fullResponse.String())
		s.doBilling(ctx, userID, &botmodel.Bot{Provider: "platform", Model: "coordinator"}, inputTokens, outputTokens)
	}

	return nil
}

func (s *botServiceImpl) WithTransaction(ctx context.Context, fn func(txService BotService) error) error {
	return s.repo.WithTransaction(ctx, func(txRepo dao.BotRepository) error {
		txService := NewBotService(txRepo, s.agentManager, s.einoManager, s.billingService, s.vectorService, s.searchService, s.outboxRepo, s.mcpClient, s.cfg, s.knowledgeService, s.graphSearchSvc)
		return fn(txService)
	})
}

func (s *botServiceImpl) getOrCreateAgentWithMemory(ctx context.Context, bot *botmodel.Bot, userID string) (agent.BotAgent, error) {
	enhancedPrompt := s.buildMemoryEnhancedSystemPrompt(ctx, bot, userID)

	cfg := &agent.AgentConfig{
		ID:            bot.ID,
		Name:          bot.Name,
		Description:   bot.Description,
		SystemPrompt:  enhancedPrompt,
		MaxIterations: 10,
	}

	enableRag := false
	if configVal, ok := bot.Config["enable_rag"]; ok {
		enableRag = fmt.Sprintf("%v", configVal) == "true"
	}

	if enableRag {
		kbInfo, kbTools := s.buildKnowledgeConfig(ctx, bot)
		if kbInfo != "" {
			cfg.KnowledgeBaseInfo = kbInfo
		}
		if len(kbTools) > 0 {
			cfg.Tools = kbTools
		}
	}

	mcpTools := botTools.BuildMCPTools(s.mcpRegistry)
	if len(mcpTools) > 0 {
		cfg.Tools = append(cfg.Tools, mcpTools...)
	}

	extMCPTools := s.buildExternalMCPTools(ctx)
	if len(extMCPTools) > 0 {
		cfg.Tools = append(cfg.Tools, extMCPTools...)
	}

	usePlatformModel := bot.Provider == "platform" || bot.Provider == "" || bot.APIKey == ""

	if !usePlatformModel && bot.APIKey != "" {
		chatModel, err := s.createChatModelForBot(bot)
		if err != nil {
			logger.Warn("为 Bot 创建自定义 ChatModel 失败，尝试使用全局模型",
				logger.StringField("botID", bot.ID),
				logger.ErrorField(err))
		} else if chatModel != nil {
			cfg.ChatModel = chatModel
		}
	}

	botAgent, err := s.agentManager.GetOrCreateAgent(cfg)
	if err != nil {
		logger.Error("创建 Agent 失败",
			logger.StringField("botID", bot.ID),
			logger.StringField("provider", bot.Provider),
			logger.BoolField("usePlatformModel", usePlatformModel),
			logger.BoolField("enableRag", enableRag),
			logger.ErrorField(err))
		return nil, fmt.Errorf("Agent 创建失败: %w", err)
	}

	return botAgent, nil
}

func (s *botServiceImpl) buildKnowledgeConfig(ctx context.Context, bot *botmodel.Bot) (string, []tool.BaseTool) {
	collectionIDs := s.getCollectionIDs(bot)
	if len(collectionIDs) == 0 && s.vectorService == nil {
		return "", nil
	}

	var kbCollections []*botTools.CollectionInfo
	if s.vectorService != nil {
		listCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		collections, err := s.vectorService.ListCollections(listCtx)
		cancel()
		if err != nil {
			logger.Warn("获取知识库列表失败，使用缓存", logger.ErrorField(err))
			if cached, ok := s.kbCache.Load(bot.ID); ok {
				kbCollections = cached.([]*botTools.CollectionInfo)
			}
		} else {
			idSet := make(map[string]bool)
			for _, cid := range collectionIDs {
				idSet[cid] = true
			}
			for _, coll := range collections {
				if len(idSet) == 0 || idSet[coll.ID] {
					kbCollections = append(kbCollections, &botTools.CollectionInfo{
						ID:   coll.ID,
						Name: coll.Name,
						Size: coll.Size,
					})
				}
			}
			s.kbCache.Store(bot.ID, kbCollections)
		}
	}

	for _, coll := range kbCollections {
		logger.Info("知识库集合状态",
			logger.StringField("botID", bot.ID),
			logger.StringField("collection_id", coll.ID),
			logger.StringField("name", coll.Name),
			logger.IntField("size", int(coll.Size)))
	}

	kbInfo := botTools.FormatKnowledgeBaseList(kbCollections)

	if kbInfo == "" && len(collectionIDs) > 0 {
		var sb strings.Builder
		sb.WriteString("\n以下知识库已绑定到当前对话，你可以通过工具搜索其中的内容：\n\n")
		for i, cid := range collectionIDs {
			sb.WriteString(fmt.Sprintf("%d. collection_id: `%s`\n", i+1, cid))
		}
		sb.WriteString("\n搜索建议：\n")
		sb.WriteString("- 使用 `knowledge_search` 进行语义搜索\n")
		sb.WriteString("- 使用 `grep_chunks` 进行关键词搜索\n")
		kbInfo = sb.String()
	}

	knowledgeSvc := &knowledgeSearchAdapter{
		vectorService:   s.vectorService,
		searchService:   s.searchService,
		collectionIDs:   collectionIDs,
		searchAvailable: s.searchService != nil,
	}
	kbTools := botTools.BuildKnowledgeTools(knowledgeSvc)

	enableGraph := false
	if configVal, ok := bot.Config["enable_graph"]; ok {
		enableGraph = fmt.Sprintf("%v", configVal) == "true"
	}

	if enableGraph && s.graphSearchSvc != nil && s.knowledgeService != nil {
		defaultCollectionID := ""
		if len(collectionIDs) > 0 {
			defaultCollectionID = collectionIDs[0]
		}
		graphSearchTools := botTools.BuildGraphSearchTools(s.graphSearchSvc, defaultCollectionID)
		graphWriteTools := botTools.BuildGraphWriteTools(s.knowledgeService, s.graphSearchSvc, defaultCollectionID)
		kbTools = append(kbTools, graphSearchTools...)
		kbTools = append(kbTools, graphWriteTools...)
		if len(graphSearchTools) > 0 || len(graphWriteTools) > 0 {
			allGraphTools := append(graphSearchTools, graphWriteTools...)
			kbInfo += botTools.FormatGraphToolsInfo(allGraphTools)
		}
	}

	logger.Info("为 Bot 配置知识库工具",
		logger.StringField("botID", bot.ID),
		logger.IntField("collections", len(kbCollections)),
		logger.IntField("tools", len(kbTools)))

	return kbInfo, kbTools
}

func (s *botServiceImpl) buildExternalMCPTools(ctx context.Context) []tool.BaseTool {
	buildCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	if s.mcpClient == nil {
		if s.cfg != nil {
			mcpCli, err := client.TryDialMCPWithFallback(s.cfg)
			if err != nil {
				logger.Warn("MCP客户端重连失败", logger.ErrorField(err))
				return nil
			}
			s.mcpClient = mcpCli
			logger.Info("MCP客户端重连成功")
		} else {
			return nil
		}
	}

	services, err := s.mcpClient.ListMCPServices(buildCtx, true)
	if err != nil {
		logger.Warn("获取外部MCP服务列表失败", logger.ErrorField(err))
		return nil
	}

	if len(services) == 0 {
		return nil
	}

	var svcInfos []*botTools.ExternalMCPServiceInfo
	for _, svc := range services {
		mergedHeaders := mergeAuthConfigToHeaders(svc.Headers, svc.AuthConfig)
		logger.Info("外部MCP服务信息",
			logger.StringField("service_id", svc.ID),
			logger.StringField("name", svc.Name),
			logger.StringField("url", svc.URL),
			logger.StringField("transport", svc.TransportType),
			logger.AnyField("headers", svc.Headers),
			logger.AnyField("auth_config", svc.AuthConfig),
			logger.AnyField("merged_headers", mergedHeaders))
		s.mcpClientMgr.GetOrCreateConnection(svc.ID, svc.URL, svc.TransportType, mergedHeaders)
		svcInfos = append(svcInfos, &botTools.ExternalMCPServiceInfo{
			ID:      svc.ID,
			Name:    svc.Name,
			Enabled: svc.Enabled,
		})
	}

	extTools := botTools.BuildExternalMCPTools(s.mcpClientMgr, svcInfos)
	logger.Info("构建外部MCP工具",
		logger.IntField("services", len(services)),
		logger.IntField("tools", len(extTools)))

	return extTools
}

func (s *botServiceImpl) getCollectionIDs(bot *botmodel.Bot) []string {
	var collectionIDs []string
	if configVal, ok := bot.Config["collection_ids"]; ok {
		idsStr := fmt.Sprintf("%v", configVal)
		if idsStr != "" {
			for _, cid := range strings.Split(idsStr, ",") {
				cid = strings.TrimSpace(cid)
				if cid != "" {
					collectionIDs = append(collectionIDs, cid)
				}
			}
		}
	}
	return collectionIDs
}

func (s *botServiceImpl) createChatModelForBot(bot *botmodel.Bot) (model.BaseChatModel, error) {
	providerName := bot.Provider
	if providerName == "" || providerName == "platform" {
		providerName = "platform"
	}

	registry := provider.GetProviderRegistry()
	p, err := registry.GetProvider(providerName)
	if err != nil {
		logger.Warn("Provider 不存在，尝试使用 openai 兼容模式",
			logger.StringField("provider", providerName),
			logger.ErrorField(err))
		p, err = registry.GetProvider("openai")
		if err != nil {
			return nil, err
		}
	}

	return p.NewChatModel(bot.APIKey, bot.BaseURL, bot.Model)
}

func (s *botServiceImpl) buildMemoryEnhancedSystemPrompt(ctx context.Context, bot *botmodel.Bot, userID string) string {
	basePrompt := bot.SystemPrompt
	if basePrompt == "" {
		basePrompt = "你是一个有用的AI助手。"
	}

	if s.einoManager == nil || !s.einoManager.IsInitialized() || s.repo == nil {
		return basePrompt
	}

	memMgr := botmemory.GetMemoryManager(s.repo, s.einoManager, s.agentManager)
	memoryPrompt := memMgr.BuildMemoryPrompt(ctx, userID, bot.ID)
	if memoryPrompt == "" {
		return basePrompt
	}

	return basePrompt + "\n\n" + memoryPrompt
}

func (s *botServiceImpl) buildHistoryMessages(ctx context.Context, conversationID string, limit int) ([]*schema.Message, error) {
	messages, err := s.repo.GetMessages(ctx, conversationID, limit, nil)
	if err != nil {
		return nil, err
	}

	logger.Info("构建历史消息",
		logger.StringField("conversationID", conversationID),
		logger.IntField("count", len(messages)))

	var schemaMessages []*schema.Message
	var lastRole string
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		switch msg.Role {
		case "user":
			if lastRole == "user" {
				continue
			}
			schemaMessages = append(schemaMessages, schema.UserMessage(msg.Content))
			lastRole = "user"
		case "assistant":
			if msg.Content == "" {
				logger.Warn("跳过空的assistant消息",
					logger.StringField("msg_id", msg.ID))
				continue
			}
			schemaMessages = append(schemaMessages, schema.AssistantMessage(msg.Content, nil))
			lastRole = "assistant"
		}
	}

	return schemaMessages, nil
}

func (s *botServiceImpl) saveAssistantMessage(ctx context.Context, botID, conversationID, content string) {
	cleanContent := strutil.CleanInvalidUTF8(content)
	if cleanContent == "" {
		logger.Warn("跳过保存空助理消息",
			logger.StringField("conversationID", conversationID))
		return
	}

	assistantMsg := &botmodel.Message{
		ID:             uuid.NewString(),
		SenderID:       botID,
		BotID:          botID,
		ConversationID: conversationID,
		ChatID:         conversationID,
		Role:           "assistant",
		Content:        cleanContent,
		MentionUserIDs: botmodel.StringSlice{},
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	if err := s.repo.AddMessage(ctx, assistantMsg); err != nil {
		logger.Warn("保存助理消息失败", logger.ErrorField(err))
	}

	s.triggerMemoryExtraction(ctx, botID, conversationID)
}

func (s *botServiceImpl) publishBotEvent(ctx context.Context, action, botID, userID string, metadata map[string]string) {
	event := map[string]any{
		"action":    action,
		"bot_id":    botID,
		"user_id":   userID,
		"metadata":  metadata,
		"timestamp": time.Now().Unix(),
	}

	if err := s.outboxRepo.Save(ctx, s.repo.DB(), "bot_events", botID, event); err != nil {
		logger.Warn("保存 Bot 事件到 outbox 失败",
			logger.StringField("action", action),
			logger.StringField("botID", botID),
			logger.ErrorField(err))
	}
}

func estimateTokenCount(text string) int {
	charCount := utf8.RuneCountInString(text)
	asciiCount := max(0, len(text)-charCount*3)
	nonAsciiCount := charCount - asciiCount
	// 1个英文字符 ≈ 0.3 token，1个中文字符 ≈ 0.6 token
	return int(float64(asciiCount)*0.3+float64(nonAsciiCount)*0.6) + 50
}

// doBilling 统一计费逻辑，始终计费（不区分 usePlatformModel）
func (s *botServiceImpl) doBilling(ctx context.Context, userID string, bot *botmodel.Bot, inputTokens, outputTokens int) float64 {
	if s.billingService == nil {
		logger.Warn("计费跳过：billingService未初始化")
		return 0
	}

	billingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	provider := bot.Provider
	if provider == "" {
		provider = "custom"
	}
	if provider == "openai" && bot.Model != "" {
		if strings.HasPrefix(bot.Model, "deepseek") {
			provider = "deepseek"
		} else if strings.HasPrefix(bot.Model, "claude") {
			provider = "claude"
		} else if strings.HasPrefix(bot.Model, "ernie") || strings.HasPrefix(bot.Model, "wenxin") {
			provider = "qianfan"
		}
	}

	totalTokens := inputTokens + outputTokens
	logger.Info("开始计费",
		logger.StringField("userID", userID),
		logger.StringField("provider", provider),
		logger.StringField("model", bot.Model),
		logger.IntField("inputTokens", inputTokens),
		logger.IntField("outputTokens", outputTokens))

	cost, err := s.billingService.ConsumeModelCall(billingCtx, userID, provider, bot.Model, totalTokens, map[string]string{
		"bot_id":        bot.ID,
		"input_tokens":  fmt.Sprintf("%d", inputTokens),
		"output_tokens": fmt.Sprintf("%d", outputTokens),
	})
	if err != nil {
		logger.Warn("计费调用失败，继续处理", logger.ErrorField(err))
		return 0
	}
	logger.Info("计费成功", logger.Float64Field("cost", cost))
	return cost
}

type knowledgeSearchAdapter struct {
	vectorService   VectorService
	searchService   SearchService
	collectionIDs   []string
	searchAvailable bool
}

func (a *knowledgeSearchAdapter) SearchVector(ctx context.Context, collectionIDs []string, query string, topK int) ([]*botTools.KnowledgeSearchResult, error) {
	targets := collectionIDs
	if len(targets) == 0 {
		targets = a.collectionIDs
	}

	logger.Info("知识库语义搜索",
		logger.AnyField("targets", targets),
		logger.StringField("query", query),
		logger.IntField("top_k", topK),
		logger.BoolField("has_vector_service", a.vectorService != nil))

	if a.vectorService == nil {
		logger.Warn("向量服务未初始化，无法搜索")
		return nil, nil
	}

	if len(targets) == 0 {
		logger.Warn("未配置知识库集合ID，无法搜索")
		return nil, nil
	}

	var allResults []*botTools.KnowledgeSearchResult
	seen := make(map[string]bool)

	type collResult struct {
		results []*VectorSearchResult
		cid     string
		err     error
	}
	collCh := make(chan collResult, len(targets))
	var wg sync.WaitGroup

	for _, cid := range targets {
		cid = strings.TrimSpace(cid)
		if cid == "" {
			continue
		}
		wg.Add(1)
		go func(collectionID string) {
			defer wg.Done()
			searchCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
			results, err := a.vectorService.SearchWithScores(searchCtx, collectionID, query, topK)
			cancel()
			collCh <- collResult{results: results, cid: collectionID, err: err}
		}(cid)
	}

	go func() {
		wg.Wait()
		close(collCh)
	}()

	for cr := range collCh {
		if cr.err != nil {
			logger.Warn("向量搜索失败", logger.StringField("collection_id", cr.cid), logger.ErrorField(cr.err))
			continue
		}
		logger.Info("向量搜索结果",
			logger.StringField("collection_id", cr.cid),
			logger.IntField("count", len(cr.results)))
		for _, r := range cr.results {
			if !seen[r.ID] {
				seen[r.ID] = true
				metadata := r.Metadata
				if metadata == nil {
					metadata = map[string]string{}
				}
				metadata["collection_id"] = cr.cid
				allResults = append(allResults, &botTools.KnowledgeSearchResult{
					ID:       r.ID,
					Content:  r.Content,
					Score:    r.Score,
					Source:   "vector",
					Metadata: metadata,
				})
			}
		}
	}

	logger.Info("知识库语义搜索完成", logger.IntField("total_results", len(allResults)))
	return allResults, nil
}

func (a *knowledgeSearchAdapter) SearchKeyword(ctx context.Context, query string, topK int) ([]*botTools.KnowledgeSearchResult, error) {
	if a.searchService == nil {
		return nil, nil
	}

	if !a.searchAvailable {
		return nil, nil
	}

	logger.Info("知识库关键词搜索", logger.StringField("query", query), logger.IntField("top_k", topK))

	searchCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	results, err := a.searchService.Search(searchCtx, query, topK)
	cancel()
	if err != nil {
		logger.Warn("关键词搜索失败，后续请求将跳过关键词搜索", logger.ErrorField(err))
		a.searchAvailable = false
		return nil, nil
	}

	logger.Info("关键词搜索结果", logger.IntField("count", len(results)))

	var allResults []*botTools.KnowledgeSearchResult
	for _, r := range results {
		allResults = append(allResults, &botTools.KnowledgeSearchResult{
			ID:      r.ID,
			Title:   r.Title,
			Content: r.Content,
			Score:   r.Score,
			Source:  "keyword",
		})
	}

	return allResults, nil
}

func (a *knowledgeSearchAdapter) ListCollections(ctx context.Context) ([]*botTools.CollectionInfo, error) {
	if a.vectorService == nil {
		return nil, nil
	}

	listCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	collections, err := a.vectorService.ListCollections(listCtx)
	cancel()
	if err != nil {
		return nil, err
	}

	idSet := make(map[string]bool)
	for _, cid := range a.collectionIDs {
		idSet[cid] = true
	}

	var result []*botTools.CollectionInfo
	for _, coll := range collections {
		if len(idSet) == 0 || idSet[coll.ID] {
			result = append(result, &botTools.CollectionInfo{
				ID:   coll.ID,
				Name: coll.Name,
				Size: coll.Size,
			})
		}
	}

	return result, nil
}

func (s *botServiceImpl) triggerMemoryExtraction(ctx context.Context, botID, conversationID string) {
	if s.einoManager == nil || !s.einoManager.IsInitialized() {
		logger.Warn("记忆提取跳过: EinoManager未初始化", logger.StringField("botID", botID))
		return
	}

	conversation, err := s.repo.GetConversation(ctx, conversationID)
	if err != nil || conversation == nil {
		logger.Warn("记忆提取跳过: 获取对话失败", logger.StringField("conversationID", conversationID), logger.ErrorField(err))
		return
	}

	messages, err := s.repo.GetMessages(ctx, conversationID, 20, nil)
	if err != nil || len(messages) == 0 {
		logger.Warn("记忆提取跳过: 无消息", logger.StringField("conversationID", conversationID), logger.IntField("messageCount", len(messages)), logger.ErrorField(err))
		return
	}

	var chatModel model.BaseChatModel
	bot, botErr := s.repo.GetBot(ctx, botID)
	if botErr == nil && bot != nil {
		usePlatformModel := bot.Provider == "platform" || bot.Provider == "" || bot.APIKey == ""
		if !usePlatformModel && bot.APIKey != "" {
			if cm, cmErr := s.createChatModelForBot(bot); cmErr == nil && cm != nil {
				chatModel = cm
			}
		}
	}

	logger.Info("触发记忆提取", logger.StringField("botID", botID), logger.StringField("userID", conversation.UserID), logger.IntField("messageCount", len(messages)))

	memMgr := botmemory.GetMemoryManager(s.repo, s.einoManager, s.agentManager)
	memMgr.ExtractAndSaveMemories(ctx, conversation.UserID, botID, messages, chatModel)
}

func mergeAuthConfigToHeaders(headers map[string]string, authConfig map[string]string) map[string]string {
	result := make(map[string]string, len(headers)+1)
	for k, v := range headers {
		result[k] = v
	}
	if authConfig == nil {
		return result
	}
	authType := authConfig["type"]
	switch authType {
	case "bearer":
		if token := authConfig["token"]; token != "" {
			result["Authorization"] = "Bearer " + token
		}
	case "api_key":
		key := authConfig["key"]
		headerName := authConfig["header_name"]
		if headerName == "" {
			headerName = "X-API-Key"
		}
		if key != "" {
			result[headerName] = key
		}
	}
	return result
}

// updateQQBotCache 更新 QQ 号与 Bot ID 的绑定缓存
// QQ Bridge 服务通过此缓存查找绑定了 QQ 号的 Bot
func (s *botServiceImpl) updateQQBotCache(qqNumber, botID string) {
	redis := cache.NewRedisCache()
	ctx := context.Background()
	key := fmt.Sprintf("qq:bot_bind:%s", qqNumber)
	if err := redis.Set(ctx, key, botID, 0); err != nil {
		logger.Warn("写入QQ-Bot绑定缓存失败",
			logger.StringField("qq_number", qqNumber),
			logger.StringField("bot_id", botID),
			logger.ErrorField(err))
	} else {
		logger.Info("QQ-Bot绑定缓存已更新",
			logger.StringField("qq_number", qqNumber),
			logger.StringField("bot_id", botID))
	}
}
