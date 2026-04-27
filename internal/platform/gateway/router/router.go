package router

import (
	"Logos/config"
	"Logos/internal/platform/gateway/handler"
	"Logos/internal/platform/gateway/middleware"
	"Logos/pkg/cache"
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

	// 限流中间件（Redis 不可用时自动跳过）
	redisCache := cache.NewRedisCache()
	r.Use(middleware.RedisRateLimit(redisCache))
	r.Use(middleware.IPBasedRateLimit(redisCache, 200, time.Minute))
	r.Use(middleware.BurstProtection(redisCache, 1000, 5*time.Minute))

	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok", "service": "logos-gateway"})
	})

	api := r.Group("/api/v1")

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

	h := &handler.Handler{
		UserClient:       userClient,
		KnowledgeClient:  knowledgeClient,
		SearchClient:     searchClient,
		VectorClient:     vectorClient,
		QuestionClient:   questionClient,
		RecommendClient:  recommendClient,
		ExtractionClient: extractionClient,
		CollectionClient: collectionClient,
		MessageClient:    messageClient,
		MonitoringClient: monitoringClient,
	}

	auth := api.Group("")
	auth.Use(middleware.JWTAuth())

	auth.POST("/auth/login", h.Login)
	auth.POST("/auth/register", h.Register)

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
		extraction.POST("/tasks/:id/cancel", h.CancelTask)
		extraction.POST("/extract/text", h.ExtractFromText)
		extraction.GET("/results/:taskId", h.GetResults)
		extraction.GET("/results/detail/:id", h.GetResult)
	}

	collection := auth.Group("/collection")
	{
		collection.POST("/data-sources", h.AddDataSource)
		collection.GET("/data-sources", h.ListDataSources)
		collection.GET("/data-sources/:id", h.GetDataSource)
		collection.PUT("/data-sources/:id", h.UpdateDataSource)
		collection.DELETE("/data-sources/:id", h.DeleteDataSource)
		collection.POST("/tasks", h.CreateCollectionTask)
		collection.GET("/tasks", h.ListCollectionTasks)
		collection.GET("/tasks/:id", h.GetCollectionTask)
		collection.PUT("/tasks/:id", h.UpdateCollectionTask)
		collection.DELETE("/tasks/:id", h.DeleteCollectionTask)
		collection.POST("/tasks/:id/execute", h.ExecuteCollectionTask)
		collection.POST("/tasks/:id/stop", h.StopCollectionTask)
		collection.GET("/results/:taskId", h.GetCollectionResults)
		collection.GET("/results/detail/:id", h.GetCollectionResult)
	}

	message := auth.Group("/message")
	{
		message.POST("/send", h.SendMessage)
		message.POST("/send/batch", h.BatchSendMessage)
		message.POST("/subscribe", h.Subscribe)
		message.POST("/consume", h.ConsumeMessages)
		message.POST("/ack", h.AcknowledgeMessage)
		message.POST("/ack/batch", h.BatchAcknowledgeMessages)
		message.GET("/stats", h.GetMessageStats)
		message.POST("/topics/create", h.CreateTopic)
		message.DELETE("/topics/:topic", h.DeleteTopic)
		message.DELETE("/messages/clear", h.ClearMessages)
	}

	monitoring := auth.Group("/monitoring")
	{
		monitoring.POST("/metrics/record", h.RecordMetric)
		monitoring.POST("/metrics/batch", h.BatchRecordMetric)
		monitoring.GET("/metrics/query", h.QueryMetrics)
		monitoring.POST("/logs/record", h.RecordLog)
		monitoring.POST("/logs/batch", h.BatchRecordLog)
		monitoring.GET("/logs/query", h.QueryLogs)
		monitoring.GET("/alerts/query", h.QueryAlerts)
		monitoring.POST("/alerts/:alertId/resolve", h.ResolveAlert)
		monitoring.POST("/services/status", h.UpdateServiceStatus)
		monitoring.GET("/services/status", h.GetServiceStatus)
		monitoring.GET("/services/statuses", h.ListServiceStatuses)
	}

	return r
}
