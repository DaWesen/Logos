package router

import (
	"Logos/config"
	"Logos/internal/service/ai"
	"Logos/internal/service/platform/gateway/handler"
	"Logos/internal/service/platform/gateway/middleware"
	"Logos/internal/service/platform/gateway/websocket"
	"Logos/pkg/cache"
	"Logos/pkg/client"
	"Logos/pkg/logger"
	"Logos/pkg/storage"
	"os"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	httpRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_http_requests_total",
			Help: "Total number of HTTP requests",
		},
		[]string{"method", "path", "status"},
	)
	httpRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "gateway_http_request_duration_seconds",
			Help:    "HTTP request duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "path"},
	)
)

func init() {
	prometheus.MustRegister(httpRequestsTotal)
	prometheus.MustRegister(httpRequestDuration)
}

func SetupRouter(wsHandler *websocket.Handler) *gin.Engine {
	cfg := config.GetConfig()

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()

	r.Use(middleware.Recovery())
	r.Use(middleware.Logger())
	r.Use(middleware.CORS())
	r.Use(promMiddleware())
	r.Use(middleware.MetricsReporterMiddleware())

	redisCache := cache.NewRedisCache()
	r.Use(middleware.RedisRateLimit(redisCache))
	r.Use(middleware.IPBasedRateLimit(redisCache, 600000, time.Minute))
	r.Use(middleware.BurstProtection(redisCache, 100000, 5*time.Minute))

	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok", "service": "logos-gateway"})
	})

	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	r.GET("/test", func(c *gin.Context) {
		logger.Info("Test endpoint called")
		c.JSON(200, gin.H{"message": "Gateway is working!"})
	})

	r.GET("/ws", wsHandler.HandleWebSocket)

	// 初始化文件上传处理器
	minioClient, err := storage.InitMinio()
	if err != nil {
		logger.Error("Failed to init MinIO", logger.ErrorField(err))
	} else {
		logger.Info("MinIO initialized successfully")
	}
	minioManager := storage.NewMinioManager(minioClient)
	uploadHandler := ai.NewFileUploadHandler(minioManager, cfg.Minio.Bucket)

	api := r.Group("/api/v1")

	file := api.Group("/file")
	{
		file.POST("/upload", uploadHandler.UploadFile)
		file.POST("/upload/multiple", uploadHandler.UploadMultipleFiles)
		file.DELETE("", uploadHandler.DeleteFile)
		file.GET("/url", uploadHandler.GetFileURL)
		file.GET("/minio/*path", uploadHandler.ProxyMinioFile)
	}

	// 初始化 user client
	logger.Info("Initializing user client...")
	userClient, err := handler.InitUserClient(cfg)
	if err != nil {
		logger.Error("Failed to init user client", logger.ErrorField(err))
	} else {
		logger.Info("UserClient initialized successfully")
	}

	// 初始化 monitoring client
	logger.Info("Initializing monitoring client...")
	monitoringClient, err := handler.InitMonitoringClient(cfg)
	if err != nil {
		logger.Error("Failed to init monitoring client", logger.ErrorField(err))
	} else {
		logger.Info("MonitoringClient initialized successfully")
		middleware.MonitoringClient = monitoringClient
		// 初始化指标上报器（使用包装客户端）
		monitoringWrapper, wrapErr := client.NewMonitoringClientFromConfig(cfg)
		if wrapErr != nil {
			logger.Error("Failed to init monitoring wrapper client", logger.ErrorField(wrapErr))
		} else {
			middleware.InitMetricsReporter(monitoringWrapper)
		}
	}

	// 初始化 bot client
	logger.Info("Initializing bot client...")
	botClient, err := handler.InitBotClient(cfg)
	if err != nil {
		logger.Error("Failed to init bot client", logger.ErrorField(err))
	} else {
		logger.Info("BotClient initialized successfully")
	}

	// 初始化 chat client
	logger.Info("Initializing chat client...")
	chatClient, err := handler.InitChatClient(cfg)
	if err != nil {
		logger.Error("Failed to init chat client", logger.ErrorField(err))
	} else {
		logger.Info("ChatClient initialized successfully")
	}

	// 初始化 contact client
	logger.Info("Initializing contact client...")
	contactClient, err := handler.InitContactClient(cfg)
	if err != nil {
		logger.Error("Failed to init contact client", logger.ErrorField(err))
	} else {
		logger.Info("ContactClient initialized successfully")
	}

	// 初始化 billing client
	logger.Info("Initializing billing client...")
	billingClient, err := handler.InitBillingClient(cfg)
	if err != nil {
		logger.Error("Failed to init billing client", logger.ErrorField(err))
	} else {
		logger.Info("BillingClient initialized successfully")
	}

	// 初始化 knowledge client
	logger.Info("Initializing knowledge client...")
	knowledgeClient, err := handler.InitKnowledgeClient(cfg)
	if err != nil {
		logger.Error("Failed to init knowledge client", logger.ErrorField(err))
	} else {
		logger.Info("KnowledgeClient initialized successfully")
	}

	// 初始化 vector client
	logger.Info("Initializing vector client...")
	vectorClient, err := handler.InitVectorClient(cfg)
	if err != nil {
		logger.Error("Failed to init vector client", logger.ErrorField(err))
	} else {
		logger.Info("VectorClient initialized successfully")
	}

	// 初始化 search client
	logger.Info("Initializing search client...")
	searchClient, err := handler.InitSearchClient(cfg)
	if err != nil {
		logger.Error("Failed to init search client", logger.ErrorField(err))
	} else {
		logger.Info("SearchClient initialized successfully")
	}

	// 初始化 question client
	logger.Info("Initializing question client...")
	questionClient, err := handler.InitQuestionClient(cfg)
	if err != nil {
		logger.Error("Failed to init question client", logger.ErrorField(err))
	} else {
		logger.Info("QuestionClient initialized successfully")
	}

	// 初始化 recommend client
	logger.Info("Initializing recommend client...")
	recommendClient, err := handler.InitRecommendClient(cfg)
	if err != nil {
		logger.Error("Failed to init recommend client", logger.ErrorField(err))
	} else {
		logger.Info("RecommendClient initialized successfully")
	}

	// 初始化 extraction client
	logger.Info("Initializing extraction client...")
	extractionClient, err := handler.InitExtractionClient(cfg)
	if err != nil {
		logger.Error("Failed to init extraction client", logger.ErrorField(err))
	} else {
		logger.Info("ExtractionClient initialized successfully")
	}

	// 初始化 collection client
	logger.Info("Initializing collection client...")
	collectionClient, err := handler.InitCollectionClient(cfg)
	if err != nil {
		logger.Error("Failed to init collection client", logger.ErrorField(err))
	} else {
		logger.Info("CollectionClient initialized successfully")
	}

	// 初始化 summary client
	logger.Info("Initializing summary client...")
	summaryClient, err := handler.InitSummaryClient(cfg)
	if err != nil {
		logger.Error("Failed to init summary client", logger.ErrorField(err))
	} else {
		logger.Info("SummaryClient initialized successfully")
	}

	// 初始化 mcp client
	logger.Info("Initializing mcp client...")
	mcpClient, err := handler.InitMCPClient(cfg)
	if err != nil {
		logger.Error("Failed to init mcp client", logger.ErrorField(err))
	} else {
		logger.Info("MCPClient initialized successfully")
	}

	// 初始化 moderation client
	logger.Info("Initializing moderation client...")
	moderationClient, err := handler.InitModerationClient(cfg)
	if err != nil {
		logger.Error("Failed to init moderation client", logger.ErrorField(err))
	} else {
		logger.Info("ModerationClient initialized successfully")
	}

	processPort := cfg.Ports.Process
	if processPort == 0 {
		processPort = 8090
	}
	processHost := os.Getenv("PROCESS_SERVICE_HOST")
	if processHost == "" {
		processHost = "localhost"
	}
	processServiceURL := "http://" + processHost + ":" + strconv.Itoa(int(processPort))

	h := &handler.Handler{
		UserClient:        userClient,
		MonitoringClient:  monitoringClient,
		BotClient:         botClient,
		ChatClient:        chatClient,
		ContactClient:     contactClient,
		BillingClient:     billingClient,
		KnowledgeClient:   knowledgeClient,
		VectorClient:      vectorClient,
		SearchClient:      searchClient,
		QuestionClient:    questionClient,
		RecommendClient:   recommendClient,
		ExtractionClient:  extractionClient,
		CollectionClient:  collectionClient,
		SummaryClient:     summaryClient,
		MCPClient:         mcpClient,
		ModerationClient:  moderationClient,
		WebSocketHandler:  wsHandler,
		ProcessServiceURL: processServiceURL,
		MinioManager:      minioManager,
		Cfg:               cfg,
	}

	// 公开接口（不需要认证）
	api.POST("/auth/login", h.Login)
	api.POST("/auth/register", h.Register)
	api.GET("/auth/check-username", h.CheckUsername)
	api.POST("/auth/check-username", h.CheckUsername)

	// 需要认证的接口
	authApi := api.Group("")
	authApi.Use(middleware.Auth())
	{
		// 用户相关
		authApi.GET("/users/:id", h.GetUser)
		authApi.GET("/users/username/:username", h.GetUserByUsername)
		authApi.POST("/users/batch", h.BatchGetUsers)
		authApi.PUT("/users", h.UpdateUser)
		authApi.POST("/users/avatar", h.UpdateAvatar)
		authApi.GET("/users/search", h.SearchUsers)
		authApi.POST("/users/search", h.SearchUsers)
		authApi.GET("/users/stats", h.GetUserStats)

		// 监控相关
		monitoring := authApi.Group("/monitoring")
		{
			// 指标管理
			monitoring.POST("/metric", h.RecordMetric)
			monitoring.POST("/metric/batch", h.BatchRecordMetric)
			monitoring.GET("/metric", h.QueryMetrics)

			// 日志管理
			monitoring.POST("/log", h.RecordLog)
			monitoring.POST("/log/batch", h.BatchRecordLog)
			monitoring.GET("/log", h.QueryLogs)

			// 告警管理
			monitoring.GET("/alert", h.QueryAlerts)
			monitoring.PUT("/alert/:alertId/resolve", h.ResolveAlert)

			// 服务状态
			monitoring.PUT("/service-status", h.UpdateServiceStatus)
			monitoring.GET("/service-status", h.GetServiceStatus)
			monitoring.GET("/service-status/list", h.ListServiceStatuses)
			monitoring.GET("/services", h.ListServiceInfo)
		}

		// Bot 相关
		bot := authApi.Group("/bot")
		{
			// Bot CRUD
			bot.GET("", h.ListBots)
			bot.GET("/:id", h.GetBot)
			bot.POST("", h.CreateBot)
			bot.PUT("/:id", h.UpdateBot)
			bot.DELETE("/:id", h.DeleteBot)

			// Bot 对话
			bot.POST("/message", h.SendBotMessage)
			bot.GET("/history", h.GetBotHistory)

			// Bot 记忆
			bot.GET("/memory", h.GetUserMemory)
			bot.POST("/memory", h.SetUserMemory)
			bot.DELETE("/memory", h.DeleteUserMemory)
		}

		// Chat 相关
		chat := authApi.Group("/chat")
		{
			// 聊天消息
			chat.POST("/message", h.SendChatMessage)
			chat.POST("/media", h.SendMediaMessage)
			chat.POST("/upload", h.UploadChatMedia)
			chat.GET("/history", h.GetChatHistory)
			chat.POST("/search", h.SearchChatMessages)

			// 消息操作
			chat.POST("/translate", h.TranslateChatMessage)
			chat.POST("/mark-read", h.MarkChatMessagesRead)
			chat.POST("/withdraw", h.WithdrawChatMessage)
			chat.PUT("/edit", h.EditChatMessage)
			chat.POST("/forward", h.ForwardMessage)
			chat.POST("/delete", h.DeleteChat)
			chat.POST("/delete-history", h.DeleteChatHistory)

			// 会话列表
			chat.GET("/conversations", h.GetConversationList)
			chat.GET("/unread", h.GetUnreadCount)

			// 群聊相关
			chat.POST("/group", h.CreateChatGroup)
			chat.GET("/group", h.GetChatGroup)
			chat.POST("/group/join", h.JoinChatGroup)
			chat.POST("/group/leave", h.LeaveChatGroup)
			chat.POST("/group/invite", h.InviteGroupMember)
			chat.POST("/group/kick", h.KickGroupMember)
			chat.DELETE("/group/member", h.KickGroupMember)
			chat.POST("/group/mute", h.MuteGroupMember)
			chat.POST("/group/transfer", h.TransferGroupOwner)
			chat.POST("/group/announcement", h.UpdateGroupAnnouncement)
			chat.POST("/group/avatar", h.UpdateGroupAvatar)
			chat.POST("/group/admin", h.SetGroupAdmin)
			chat.GET("/group/members", h.GetGroupMembers)
		}

		// 好友/联系人相关
		contact := authApi.Group("/contact")
		{
			contact.POST("/add", h.AddFriend)
			contact.POST("/handle", h.HandleFriendRequest)
			contact.GET("/requests", h.GetFriendRequests)
			contact.GET("/list", h.GetFriendList)
			contact.DELETE("/delete", h.DeleteFriend)
			contact.GET("/check", h.CheckFriendship)
			contact.PUT("/remark", h.UpdateFriendRemark)
			contact.POST("/group", h.CreateFriendGroup)
			contact.DELETE("/group", h.DeleteFriendGroup)
			contact.PUT("/group", h.UpdateFriendGroup)
			contact.GET("/groups", h.GetFriendGroups)
			contact.POST("/group/move", h.MoveFriendToGroup)
			contact.POST("/block", h.BlockUser)
			contact.DELETE("/block", h.UnblockUser)
			contact.GET("/blacklist", h.GetBlacklist)
		}

		// 计费相关
		billing := authApi.Group("/billing")
		{
			billing.POST("/deposit", h.Deposit)
			billing.POST("/withdraw", h.Withdraw)
			billing.POST("/refund", h.Refund)
			billing.GET("/account", h.GetAccount)
			billing.GET("/transactions", h.GetTransactions)
			billing.GET("/usage-stats", h.GetUsageStats)
			billing.POST("/consume/model-call", h.ConsumeModelCall)
			billing.POST("/consume/embedding", h.ConsumeEmbedding)
			billing.POST("/consume/storage", h.ConsumeStorage)
			billing.POST("/consume/bandwidth", h.ConsumeBandwidth)
		}

		// 向量服务相关
		vector := authApi.Group("/vector")
		{
			// 集合管理
			vector.POST("/collections", h.CreateCollection)
			vector.GET("/collections", h.ListCollections)
			vector.GET("/collections/:id", h.GetCollection)
			vector.PUT("/collections/:id", h.UpdateCollection)
			vector.DELETE("/collections/:id", h.DeleteCollection)

			// 向量操作
			vector.POST("/vectorize", h.Vectorize)
			vector.POST("/vectorize/batch", h.BatchVectorize)
			vector.POST("/search", h.VectorSearchByCollection)
			vector.POST("/text-search", h.TextSearchByCollection)
			vector.DELETE("/vectors", h.DeleteVector)
			vector.DELETE("/vectors/batch", h.BatchDeleteVector)
			vector.GET("/collections/:id/vectors", h.ListVectors)
		}

		// 知识图谱相关
		knowledge := authApi.Group("/knowledge")
		{
			// 实体管理
			knowledge.POST("/entities", h.AddEntity)
			knowledge.PUT("/entities/:id", h.UpdateEntity)
			knowledge.DELETE("/entities/:id", h.DeleteEntity)
			knowledge.GET("/entities/:id", h.GetEntity)
			knowledge.GET("/entities", h.QueryEntities)
			knowledge.GET("/entities/search", h.SearchEntities)
			knowledge.DELETE("/entities/clear", h.ClearEntities)

			// 关系管理
			knowledge.POST("/relations", h.AddRelation)
			knowledge.PUT("/relations/:id", h.UpdateRelation)
			knowledge.DELETE("/relations/:id", h.DeleteRelation)
			knowledge.GET("/relations/:id", h.GetRelation)
			knowledge.GET("/relations", h.QueryRelations)

			// 知识图谱操作
			knowledge.GET("/stats", h.GetGraphStats)
			knowledge.GET("/entities/related/:entityId", h.GetRelatedEntities)
			knowledge.POST("/import", h.ImportData)

			// 图谱遍历
			knowledge.GET("/entities/:id/subgraph", h.GetSubgraph)
			knowledge.GET("/paths", h.GetEntityPaths)
		}

		search := authApi.Group("/search")
		{
			search.POST("/query", h.Search)
			search.POST("/documents", h.AddDocument)
			search.PUT("/documents/:id", h.UpdateDocument)
			search.DELETE("/documents/:id", h.DeleteDocument)
			search.GET("/documents/:id", h.GetDocument)
			search.POST("/documents/batch", h.BatchAddDocuments)
			search.DELETE("/documents/batch", h.BatchDeleteDocuments)
			search.POST("/indices", h.CreateIndex)
			search.DELETE("/indices/:id", h.DeleteIndex)
			search.POST("/indices/:id/refresh", h.RefreshIndex)
			search.GET("/indices/:id/stats", h.GetIndexStats)
		}

		qa := authApi.Group("/qa")
		{
			qa.POST("/ask", h.AskQuestion)
			qa.POST("/ask/batch", h.BatchAskQuestions)
			qa.GET("/history", h.GetHistory)
			qa.POST("/feedback", h.SubmitFeedback)
			qa.GET("/recommended", h.GetRecommendedQuestions)
		}

		recommend := authApi.Group("/recommend")
		{
			recommend.GET("/items", h.GetRecommendations)
			recommend.GET("/related", h.GetRelatedRecommendations)
			recommend.POST("/feedback", h.SubmitRecommendFeedback)
			recommend.GET("/history", h.GetRecommendationHistory)
			recommend.POST("/batch", h.BatchGetRecommendations)
		}

		extraction := authApi.Group("/extraction")
		{
			extraction.POST("/tasks", h.CreateTask)
			extraction.GET("/tasks", h.ListTasks)
			extraction.GET("/tasks/:id", h.GetTask)
			extraction.PUT("/tasks/:id", h.UpdateTask)
			extraction.DELETE("/tasks/:id", h.DeleteTask)
			extraction.POST("/tasks/:id/execute", h.ExecuteTask)
			extraction.POST("/tasks/:id/cancel", h.CancelTask)
			extraction.POST("/extract", h.ExtractFromText)
			extraction.GET("/results", h.GetResults)
			extraction.GET("/results/:id", h.GetResult)
		}

		dataCollection := authApi.Group("/collection")
		{
			dataCollection.POST("/sources", h.AddDataSource)
			dataCollection.GET("/sources", h.ListDataSources)
			dataCollection.GET("/sources/:id", h.GetDataSource)
			dataCollection.PUT("/sources/:id", h.UpdateDataSource)
			dataCollection.DELETE("/sources/:id", h.DeleteDataSource)
			dataCollection.POST("/tasks", h.CreateCollectionTask)
			dataCollection.GET("/tasks", h.ListCollectionTasks)
			dataCollection.GET("/tasks/:id", h.GetCollectionTask)
			dataCollection.PUT("/tasks/:id", h.UpdateCollectionTask)
			dataCollection.DELETE("/tasks/:id", h.DeleteCollectionTask)
			dataCollection.POST("/tasks/:id/execute", h.ExecuteCollectionTask)
			dataCollection.POST("/tasks/:id/stop", h.StopCollectionTask)
			dataCollection.GET("/results", h.GetCollectionResults)
			dataCollection.GET("/results/:id", h.GetCollectionResult)
		}

		summary := authApi.Group("/summary")
		{
			summary.POST("/summarize", h.SummarizeMessages)
			summary.POST("/reply-candidates", h.GenerateReplyCandidates)
			summary.POST("/extract-todos", h.ExtractTodos)
		}

		mcp := authApi.Group("/mcp")
		{
			mcp.POST("/call", h.CallTool)
			mcp.POST("/register", h.RegisterTool)
			mcp.POST("/list", h.ListMCPTools)
			mcp.GET("/tools/:id", h.GetMCPTool)
			mcp.PUT("/tools/:id", h.UpdateMCPTool)
			mcp.DELETE("/tools/:id", h.DeleteMCPTool)

			mcp.POST("/services", h.CreateMCPService)
			mcp.POST("/services/list", h.ListMCPServices)
			mcp.GET("/services/:id", h.GetMCPService)
			mcp.PUT("/services/:id", h.UpdateMCPService)
			mcp.DELETE("/services/:id", h.DeleteMCPService)
			mcp.POST("/services/test", h.TestMCPService)
		}

		process := authApi.Group("/process")
		{
			process.Any("/*any", h.ProxyProcessService)
		}
	}

	return r
}

func promMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.FullPath()
		if path == "" || path == "/metrics" {
			c.Next()
			return
		}
		c.Next()

		status := strconv.Itoa(c.Writer.Status())
		duration := time.Since(start).Seconds()

		httpRequestsTotal.WithLabelValues(c.Request.Method, path, status).Inc()
		httpRequestDuration.WithLabelValues(c.Request.Method, path).Observe(duration)
	}
}
