package main

import (
	"Logos/config"
	"Logos/internal/service/ai/extraction/dao"
	"Logos/internal/service/ai/extraction/handler"
	"Logos/internal/service/ai/extraction/model"
	"Logos/internal/service/ai/extraction/service"
	"Logos/pkg/client"
	"Logos/pkg/database/pgsql"
	"Logos/pkg/eino"
	"Logos/pkg/grpcserver"
	"Logos/pkg/logger"
	"Logos/pkg/obs"
	pb "Logos/proto_gen/extraction"
	"context"
	"log"

	"google.golang.org/grpc"
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

	var einoClient *eino.EinoManager
	einoClient, err = eino.InitEino()
	if err != nil {
		log.Printf("Failed to init eino: %v", err)
	}

	repo := dao.NewExtractionRepository(db)

	var knowledgeService service.KnowledgeService
	knowledgeRawClient, knowledgeErr := client.NewKnowledgeClientFromConfig(cfg)
	if knowledgeErr != nil {
		logger.Warn("连接Knowledge服务失败", logger.ErrorField(knowledgeErr))
	} else {
		knowledgeService = service.NewKnowledgeClientAdapter(knowledgeRawClient)
		logger.Info("Knowledge服务客户端已连接")
	}

	var vectorService service.VectorService
	vectorRawClient, vectorErr := client.NewVectorClientFromConfig(cfg)
	if vectorErr != nil {
		logger.Warn("连接Vector服务失败", logger.ErrorField(vectorErr))
	} else {
		vectorService = service.NewVectorClientAdapter(vectorRawClient)
		logger.Info("Vector服务客户端已连接")
	}

	extractionService := service.NewExtractionService(repo, einoClient, knowledgeService, vectorService)

	if err := extractionService.StartKafkaConsumer(context.Background()); err != nil {
		logger.Warn("启动Kafka消费者失败", logger.ErrorField(err))
	}

	shutdown, serverOpt, _ := obs.InitGRPCProvider("extraction")
	defer shutdown(context.Background())

	if err := grpcserver.StartServer(grpcserver.ServerConfig{
		ServiceName: "logos.extraction",
		Port:        cfg.Ports.Extraction,
		Etcd:        grpcserver.EtcdConfig{Endpoints: cfg.Etcd.Endpoints},
	}, func(s *grpc.Server) {
		pb.RegisterKnowledgeExtractionServiceServer(s, &handler.KnowledgeExtractionServiceImpl{
			ExtractionService: extractionService,
		})
	}, serverOpt); err != nil {
		log.Fatalf("Extraction service failed to run: %v", err)
	}
}
