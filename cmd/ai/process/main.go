package main

import (
	"fmt"
	"log"

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
	extractionRawClient, extractionErr := client.NewExtractionClientFromConfig(cfg)
	if extractionErr != nil {
		logger.Warn("连接Extraction服务失败", logger.ErrorField(extractionErr))
	} else {
		extractionService = service.NewExtractionClientAdapter(extractionRawClient)
		logger.Info("Extraction服务客户端已连接")
	}

	var vectorService service.VectorService
	vectorRawClient, vectorErr := client.NewVectorClientFromConfig(cfg)
	if vectorErr != nil {
		logger.Warn("连接Vector服务失败", logger.ErrorField(vectorErr))
	} else {
		vectorService = service.NewVectorClientAdapter(vectorRawClient)
		logger.Info("Vector服务客户端已连接")
	}

	var knowledgeService service.KnowledgeService
	knowledgeRawClient, knowledgeErr := client.NewKnowledgeClientFromConfig(cfg)
	if knowledgeErr != nil {
		logger.Warn("连接Knowledge服务失败", logger.ErrorField(knowledgeErr))
	} else {
		knowledgeService = service.NewKnowledgeClientAdapter(knowledgeRawClient)
		logger.Info("Knowledge服务客户端已连接")
	}

	processConfig := &service.Config{
		ProcessPort:      cfg.Ports.Process,
		VectorCollection: "documents",
	}

	processService := service.NewProcessService(repo, extractionService, vectorService, knowledgeService, vlmModel, asrModel, videoExtractor, processConfig)

	processHandler := handler.NewProcessHandler(processService)

	r := gin.Default()
	processHandler.RegisterRoutes(r)
	processHandler.RegisterMiddlewares(r)

	port := cfg.Ports.Process
	if port == 0 {
		port = 8090
	}

	logger.Info("Process service starting", logger.IntField("port", port))
	if err := r.Run(fmt.Sprintf(":%d", port)); err != nil {
		log.Fatalf("Process service failed to run: %v", err)
	}
}
