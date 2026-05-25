package main

import (
	"fmt"
	"log"
	"time"

	"Logos/config"
	"Logos/internal/models/asr"
	"Logos/internal/models/video"
	"Logos/internal/models/vlm"
	"Logos/internal/service/ai/process/dao"
	"Logos/internal/service/ai/process/handler"
	"Logos/internal/service/ai/process/model"
	"Logos/internal/service/ai/process/service"
	"Logos/pkg/client"
	"Logos/pkg/database/pgsql"
	"Logos/pkg/logger"

	"github.com/gin-gonic/gin"
)

func ginLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		method := c.Request.Method

		c.Next()

		latency := time.Since(start)
		statusCode := c.Writer.Status()

		logger.Info("HTTP请求",
			logger.StringField("method", method),
			logger.StringField("path", path),
			logger.IntField("status", statusCode),
			logger.StringField("latency", latency.String()),
		)
	}
}

func ginRecovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				logger.Error("请求panic",
					logger.StringField("path", c.Request.URL.Path),
					logger.AnyField("error", err),
				)
				c.AbortWithStatusJSON(500, gin.H{"error": "内部服务器错误"})
			}
		}()
		c.Next()
	}
}

func main() {
	cfg := config.GetConfig()

	logger.InitLogger()

	db, err := pgsql.InitPostgres()
	if err != nil {
		log.Fatalf("Failed to init postgres: %v", err)
	}

	if err := model.AutoMigrate(db); err != nil {
		log.Fatalf("Failed to auto migrate: %v", err)
	}

	repo := dao.NewProcessRepository(db)

	var vlmModel vlm.VLM
	var asrModel asr.ASR
	var videoExtractor video.Extractor

	if cfg.Eino.APIKey != "" {
		vlmConfig := &vlm.Config{
			Source:    "remote",
			ModelName: cfg.Eino.Model,
			BaseURL:   cfg.Eino.BaseURL,
			APIKey:    cfg.Eino.APIKey,
		}
		vlmModel, _ = vlm.NewRemoteAPIVLM(vlmConfig)

		asrConfig := &asr.Config{
			Source:    "remote",
			BaseURL:   cfg.Eino.BaseURL,
			APIKey:    cfg.Eino.APIKey,
			ModelName: "whisper-1",
		}
		asrModel, _ = asr.NewOpenAIASR(asrConfig)

		videoConfig := &video.Config{
			TempDir: "./tmp",
		}
		videoExtractor, _ = video.NewFFMpegExtractor(videoConfig)
	}

	var extractionService service.ExtractionService
	extractionRawClient, extractionErr := client.TryDialExtractionWithFallback(cfg)
	if extractionErr != nil {
		logger.Warn("连接Extraction服务失败", logger.ErrorField(extractionErr))
	} else {
		extractionService = service.NewExtractionClientAdapter(extractionRawClient)
		logger.Info("Extraction服务客户端已连接（直连模式）")
	}

	var vectorService service.VectorService
	vectorRawClient, vectorErr := client.TryDialVectorWithFallback(cfg)
	if vectorErr != nil {
		logger.Warn("连接Vector服务失败", logger.ErrorField(vectorErr))
	} else {
		vectorService = service.NewVectorClientAdapter(vectorRawClient)
		logger.Info("Vector服务客户端已连接（直连模式）")
	}

	var knowledgeService service.KnowledgeService
	knowledgeRawClient, knowledgeErr := client.TryDialKnowledgeWithFallback(cfg)
	if knowledgeErr != nil {
		logger.Warn("连接Knowledge服务失败", logger.ErrorField(knowledgeErr))
	} else {
		knowledgeService = service.NewKnowledgeClientAdapter(knowledgeRawClient)
		logger.Info("Knowledge服务客户端已连接（直连模式）")
	}

	processConfig := &service.Config{
		ProcessPort:      cfg.Ports.Process,
		VectorCollection: "documents",
	}

	processService := service.NewProcessService(repo, extractionService, vectorService, knowledgeService, vlmModel, asrModel, videoExtractor, processConfig)

	processHandler := handler.NewProcessHandler(processService)

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(ginRecovery())
	r.Use(ginLogger())

	processHandler.RegisterRoutes(r)

	port := cfg.Ports.Process
	if port == 0 {
		port = 8090
	}

	logger.Info("Process service starting", logger.IntField("port", port))
	if err := r.Run(fmt.Sprintf(":%d", port)); err != nil {
		log.Fatalf("Process service failed to run: %v", err)
	}
}
