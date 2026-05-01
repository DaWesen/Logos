package router

import (
	"Logos/config"
	"Logos/internal/service/ai"
	"Logos/internal/service/platform/gateway/handler"
	"Logos/internal/service/platform/gateway/middleware"
	"Logos/internal/service/platform/gateway/websocket"
	"Logos/pkg/cache"
	"Logos/pkg/storage"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
)

func SetupRouter() *gin.Engine {
	cfg := config.GetConfig()

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()

	r.Use(middleware.Recovery())
	r.Use(middleware.Logger())
	r.Use(middleware.CORS())

	redisCache := cache.NewRedisCache()
	r.Use(middleware.RedisRateLimit(redisCache))
	r.Use(middleware.IPBasedRateLimit(redisCache, 200, time.Minute))
	r.Use(middleware.BurstProtection(redisCache, 1000, 5*time.Minute))

	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok", "service": "logos-gateway"})
	})

	wsHandler := websocket.NewHandler()
	r.GET("/ws", wsHandler.HandleWebSocket)

	// 初始化文件上传处理器
	minioClient, _ := storage.InitMinio()
	minioManager := storage.NewMinioManager(minioClient)
	uploadHandler := ai.NewFileUploadHandler(minioManager, cfg.Minio.Bucket)

	api := r.Group("/api/v1")

	// 文件上传（公开或验证都可以）
	file := api.Group("/file")
	{
		file.POST("/upload", uploadHandler.UploadFile)
		file.POST("/upload/multiple", uploadHandler.UploadMultipleFiles)
		file.DELETE("", uploadHandler.DeleteFile)
		file.GET("/url", uploadHandler.GetFileURL)
	}

	userClient, _ := handler.InitUserClient(cfg)
	knowledgeClient, _ := handler.InitKnowledgeClient(cfg)
	searchClient, _ := handler.InitSearchClient(cfg)
	vectorClient, _ := handler.InitVectorClient(cfg)
	questionClient, _ := handler.InitQuestionClient(cfg)
	recommendClient, _ := handler.InitRecommendClient(cfg)
	extractionClient, _ := handler.InitExtractionClient(cfg)
	collectionClient, _ := handler.InitCollectionClient(cfg)
	messageClient, _ := handler.InitMessageClient(cfg)
	monitoringClient, _ := handler.InitMonitoringClient(cfg)
	botClient, _ := handler.InitBotClient(cfg)
	billingClient, _ := handler.InitBillingClient(cfg)
	imClient, _ := handler.InitIMClient(cfg)
	chatClient, _ := handler.InitChatClient(cfg)
	summaryClient, _ := handler.InitSummaryClient(cfg)
	mcpClient, _ := handler.InitMCPClient(cfg)
	moderationClient, _ := handler.InitModerationClient(cfg)

	processPort := cfg.Ports.Process
	if processPort == 0 {
		processPort = 8090
	}
	processServiceURL := "http://localhost:" + fmt.Sprintf("%d", processPort)

	h := &handler.Handler{
		UserClient:        userClient,
		KnowledgeClient:   knowledgeClient,
		SearchClient:      searchClient,
		VectorClient:      vectorClient,
		QuestionClient:    questionClient,
		RecommendClient:   recommendClient,
		ExtractionClient:  extractionClient,
		CollectionClient:  collectionClient,
		MessageClient:     messageClient,
		MonitoringClient:  monitoringClient,
		BotClient:         botClient,
		BillingClient:     billingClient,
		IMClient:          imClient,
		ChatClient:        chatClient,
		SummaryClient:     summaryClient,
		MCPClient:         mcpClient,
		ModerationClient:  moderationClient,
		WebSocketHandler:  wsHandler,
		ProcessServiceURL: processServiceURL,
	}

	// 公开接口（不需要认证）
	api.POST("/auth/login", h.Login)
	api.POST("/auth/register", h.Register)

	// 需要认证的接口
	auth := api.Group("")
	auth.Use(middleware.JWTAuth())

	// Process 服务路由（需要认证）
	process := auth.Group("/process")
	{
		process.POST("/file", h.ProcessProcessFile)
		process.POST("/url", h.ProcessProcessURL)
		process.GET("/documents", h.ProcessListDocuments)
		process.GET("/documents/:id", h.ProcessGetDocument)
		process.DELETE("/documents/:id", h.ProcessDeleteDocument)
		process.POST("/documents/:id/reprocess", h.ProcessReprocessDocument)
		process.GET("/documents/:id/chunks", h.ProcessGetDocumentChunks)
	}

	user := auth.Group("/users")
	{
		user.GET("/:id", h.GetUser)
		user.GET("/username/:username", h.GetUserByUsername)
		user.PUT("", h.UpdateUser)
		user.POST("/avatar", h.UpdateAvatar)
		user.POST("/check-username", h.CheckUsername)
		user.POST("/search", h.SearchUsers)
		user.GET("/stats", h.GetUserStats)
		user.POST("/batch", h.BatchGetUsers)
	}

	knowledge := auth.Group("/knowledge")
	{
		knowledge.POST("/entities", h.AddEntity)
		knowledge.PUT("/entities/:id", h.UpdateEntity)
		knowledge.DELETE("/entities/:id", h.DeleteEntity)
		knowledge.GET("/entities/:id", h.GetEntity)
		knowledge.GET("/entities", h.QueryEntities)
		knowledge.POST("/search", h.SearchEntities)
		knowledge.POST("/relations", h.AddRelation)
		knowledge.PUT("/relations/:id", h.UpdateRelation)
		knowledge.DELETE("/relations/:id", h.DeleteRelation)
		knowledge.GET("/relations/:id", h.GetRelation)
		knowledge.GET("/relations", h.QueryRelations)
		knowledge.GET("/stats", h.GetGraphStats)
		knowledge.GET("/related/:entityId", h.GetRelatedEntities)
		knowledge.POST("/import", h.ImportData)
	}

	search := auth.Group("/search")
	{
		search.POST("", h.Search)
		search.POST("/documents", h.AddDocument)
		search.PUT("/documents", h.UpdateDocument)
		search.DELETE("/documents", h.DeleteDocument)
		search.GET("/documents/:id", h.GetDocument)
		search.POST("/documents/batch", h.BatchAddDocuments)
		search.DELETE("/documents/batch", h.BatchDeleteDocuments)
		search.POST("/indexes/:type", h.CreateIndex)
		search.DELETE("/indexes/:type", h.DeleteIndex)
		search.POST("/indexes/:type/refresh", h.RefreshIndex)
		search.GET("/indexes/stats", h.GetIndexStats)
	}

	vector := auth.Group("/vector")
	{
		vector.POST("/collections", h.CreateCollection)
		vector.GET("/collections", h.ListCollections)
		vector.GET("/collections/:id", h.GetCollection)
		vector.PUT("/collections/:id", h.UpdateCollection)
		vector.DELETE("/collections/:id", h.DeleteCollection)
		vector.POST("/vectorize", h.Vectorize)
		vector.POST("/vectorize/batch", h.BatchVectorize)
		vector.POST("/search", h.VectorSearchByCollection)
		vector.POST("/text-search", h.TextSearchByCollection)
		vector.DELETE("/vectors", h.DeleteVector)
		vector.DELETE("/vectors/batch", h.BatchDeleteVector)
	}

	question := auth.Group("/question")
	{
		question.POST("/ask", h.AskQuestion)
		question.POST("/ask/batch", h.BatchAskQuestions)
		question.GET("/history", h.GetHistory)
		question.POST("/feedback", h.SubmitFeedback)
		question.GET("/recommended/:userId", h.GetRecommendedQuestions)
	}

	recommend := auth.Group("/recommend")
	{
		recommend.GET("", h.GetRecommendations)
		recommend.GET("/related/:entityId", h.GetRelatedRecommendations)
		recommend.POST("/feedback", h.SubmitRecommendFeedback)
		recommend.GET("/history", h.GetRecommendationHistory)
		recommend.POST("/batch", h.BatchGetRecommendations)
	}

	extraction := auth.Group("/extraction")
	{
		extraction.POST("/tasks", h.CreateTask)
		extraction.GET("/tasks", h.ListTasks)
		extraction.GET("/tasks/:id", h.GetTask)
		extraction.PUT("/tasks/:id", h.UpdateTask)
		extraction.DELETE("/tasks/:id", h.DeleteTask)
		extraction.POST("/tasks/:id/execute", h.ExecuteTask)
	}

	collection := auth.Group("/collection")
	{
		collection.POST("/data", h.AddDataSource)
		collection.GET("/data", h.ListDataSources)
		collection.GET("/data/:id", h.GetDataSource)
		collection.PUT("/data/:id", h.UpdateDataSource)
		collection.DELETE("/data/:id", h.DeleteDataSource)
		collection.POST("/tasks", h.CreateCollectionTask)
		collection.GET("/tasks", h.ListCollectionTasks)
		collection.GET("/tasks/:id", h.GetCollectionTask)
		collection.PUT("/tasks/:id", h.UpdateCollectionTask)
		collection.DELETE("/tasks/:id", h.DeleteCollectionTask)
		collection.POST("/tasks/:id/execute", h.ExecuteCollectionTask)
		collection.POST("/tasks/:id/stop", h.StopCollectionTask)
		collection.GET("/results", h.GetCollectionResults)
		collection.GET("/results/:id", h.GetCollectionResult)
	}

	message := auth.Group("/message")
	{
		message.POST("/send", h.SendMessage)
		message.POST("/batch-send", h.BatchSendMessage)
		message.POST("/subscribe", h.Subscribe)
		message.GET("/consume", h.ConsumeMessages)
		message.POST("/ack", h.AcknowledgeMessage)
		message.POST("/batch-ack", h.BatchAcknowledgeMessages)
		message.GET("/stats", h.GetMessageStats)
		message.POST("/topic", h.CreateTopic)
		message.DELETE("/topic/:id", h.DeleteTopic)
		message.DELETE("/clear", h.ClearMessages)
	}

	monitoring := auth.Group("/monitoring")
	{
		monitoring.POST("/metric", h.RecordMetric)
		monitoring.POST("/metric/batch", h.BatchRecordMetric)
		monitoring.GET("/metric", h.QueryMetrics)
		monitoring.POST("/log", h.RecordLog)
		monitoring.POST("/log/batch", h.BatchRecordLog)
		monitoring.GET("/log", h.QueryLogs)
		monitoring.GET("/alerts", h.QueryAlerts)
		monitoring.PUT("/alerts/:id/resolve", h.ResolveAlert)
		monitoring.PUT("/service-status", h.UpdateServiceStatus)
		monitoring.GET("/service-status/:service", h.GetServiceStatus)
		monitoring.GET("/service-statuses", h.ListServiceStatuses)
	}

	bot := auth.Group("/bot")
	{
		bot.POST("", h.CreateBot)
		bot.PUT("/:id", h.UpdateBot)
		bot.DELETE("/:id", h.DeleteBot)
		bot.GET("/:id", h.GetBot)
		bot.GET("", h.ListBots)
		bot.POST("/message", h.SendBotMessage)
		bot.GET("/history", h.GetBotHistory)
	}

	billing := auth.Group("/billing")
	{
		billing.POST("/deposit", h.Deposit)
		billing.GET("/account", h.GetAccount)
		billing.GET("/transactions", h.GetTransactions)
		billing.GET("/usage", h.GetUsageStats)
	}

	chat := auth.Group("/chat")
	{
		chat.POST("/message", h.SendChatMessage)
		chat.POST("/search", h.SearchChatMessages)
		chat.GET("/history", h.GetChatHistory)
		chat.POST("/mark-read", h.MarkChatMessagesRead)
		chat.POST("/withdraw", h.WithdrawChatMessage)
		chat.PUT("/edit", h.EditChatMessage)
		chat.POST("/group", h.CreateChatGroup)
		chat.POST("/group/invite", h.InviteGroupMember)
		chat.DELETE("/group/member", h.KickGroupMember)
		chat.PUT("/group/mute", h.MuteGroupMember)
		chat.PUT("/group/owner", h.TransferGroupOwner)
		chat.PUT("/group/announcement", h.UpdateGroupAnnouncement)
		chat.PUT("/group/admin", h.SetGroupAdmin)
		chat.GET("/group/members", h.GetGroupMembers)
		chat.POST("/group/join", h.JoinChatGroup)
		chat.POST("/group/leave", h.LeaveChatGroup)
		chat.GET("/group", h.GetChatGroup)
	}

	im := auth.Group("/im")
	{
		im.POST("/connect", h.ConnectIM)
		im.POST("/disconnect", h.DisconnectIM)
		im.GET("/online-status", h.GetOnlineStatus)
		im.PUT("/online-status", h.SetOnlineStatus)
		im.POST("/heartbeat", h.SendTypingStatus)
		im.POST("/broadcast", h.BroadcastIMMessage)
		im.GET("/offline-messages", h.SyncOfflineMessages)
		im.GET("/stream", h.StreamMessages)
	}

	summary := auth.Group("/summary")
	{
		summary.POST("/messages", h.SummarizeMessages)
		summary.POST("/reply-candidates", h.GenerateReplyCandidates)
		summary.POST("/todos", h.ExtractTodos)
	}

	mcp := auth.Group("/mcp")
	{
		mcp.POST("/call", h.CallTool)
		mcp.POST("/tools", h.RegisterTool)
		mcp.GET("/tools", h.ListMCPTools)
		mcp.GET("/tools/:id", h.GetMCPTool)
		mcp.PUT("/tools/:id", h.UpdateMCPTool)
		mcp.DELETE("/tools/:id", h.DeleteMCPTool)
	}

	moderation := auth.Group("/moderation")
	{
		moderation.POST("/translate", h.Translate)
		moderation.POST("/content", h.ModerateContent)
		moderation.GET("/records", h.GetModerationRecords)
	}

	return r
}
