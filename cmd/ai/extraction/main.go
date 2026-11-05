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
	"Logos/pkg/health"
	"Logos/pkg/logger"
	"Logos/pkg/mq"
	"Logos/pkg/obs"
	"Logos/pkg/outbox"
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

	if err := outbox.AutoMigrate(db); err != nil {
		log.Fatalf("Failed to auto migrate outbox: %v", err)
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

	outboxRepo := outbox.NewOutboxRepository()

	extractionService := service.NewExtractionService(repo, einoClient, knowledgeService, vectorService, outboxRepo)

	// 创建任务时允许带 scheduled_time，但如果没有消费者扫描到期任务，
	// 任务会永远停在「待执行」：接口返回成功、实际什么都没发生。
	scheduler := service.NewTaskScheduler(repo, extractionService)
	scheduler.Start()
	defer scheduler.Stop()

	if err := extractionService.StartKafkaConsumer(context.Background()); err != nil {
		logger.Warn("启动Kafka消费者失败", logger.ErrorField(err))
	}

	producer := mq.NewProducer()
	relay := outbox.NewRelay(db, producer)
	relay.Start()

	shutdown, serverOpt, _ := obs.InitGRPCProvider("extraction")
	defer shutdown(context.Background())

	_ = obs.InitServiceMeters("extraction")

	ready := health.ServiceRegistry(db)

	if err := grpcserver.StartServer(grpcserver.ServerConfig{
		ServiceName: "logos.extraction",
		Port:        cfg.Ports.Extraction,
		Etcd:        grpcserver.EtcdConfig{Endpoints: cfg.Etcd.Endpoints},
		ReadyCheck:  ready,
	}, func(s *grpc.Server) {
		pb.RegisterKnowledgeExtractionServiceServer(s, &handler.KnowledgeExtractionServiceImpl{
			ExtractionService: extractionService,
		})
	}, serverOpt); err != nil {
		log.Fatalf("Extraction service failed to run: %v", err)
	}
}
