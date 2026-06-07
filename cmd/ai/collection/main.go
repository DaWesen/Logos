package main

import (
	"Logos/config"
	"Logos/internal/service/ai/collection/dao"
	"Logos/internal/service/ai/collection/handler"
	"Logos/internal/service/ai/collection/model"
	"Logos/internal/service/ai/collection/service"
	"Logos/pkg/client"
	"Logos/pkg/database/pgsql"
	"Logos/pkg/grpcserver"
	"Logos/pkg/logger"
	"Logos/pkg/mq"
	"Logos/pkg/obs"
	"Logos/pkg/outbox"
	pb "Logos/proto_gen/collection"
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

	var kafkaManager *mq.KafkaManager
	kafkaManager, err = mq.NewKafkaManager(cfg.Kafka.Brokers)
	if err != nil {
		log.Printf("Failed to init kafka, will continue without kafka: %v", err)
	} else {
		_ = kafkaManager.CreateTopic(mq.TopicDataCollection)
		log.Println("Kafka initialized with data_collection topic")
	}

	repo := dao.NewCollectionRepository(db)

	outboxRepo := outbox.NewOutboxRepository()

	var knowledgeService service.KnowledgeService
	knowledgeRawClient, knowledgeErr := client.NewKnowledgeClientFromConfig(cfg)
	if knowledgeErr != nil {
		logger.Warn("连接Knowledge服务失败，知识入库不可用", logger.ErrorField(knowledgeErr))
	} else {
		knowledgeService = service.NewKnowledgeClientAdapter(knowledgeRawClient)
		logger.Info("Knowledge服务客户端已连接")
	}

	var extractionService service.ExtractionService
	extractionRawClient, extractionErr := client.NewExtractionClientFromConfig(cfg)
	if extractionErr != nil {
		logger.Warn("连接Extraction服务失败，知识提取不可用", logger.ErrorField(extractionErr))
	} else {
		extractionService = service.NewExtractionClientAdapter(extractionRawClient)
		logger.Info("Extraction服务客户端已连接")
	}

	collectionService := service.NewCollectionService(repo, knowledgeService, extractionService, outboxRepo)

	if err := collectionService.StartKafkaConsumer(context.Background()); err != nil {
		logger.Warn("启动Kafka消费者失败", logger.ErrorField(err))
	}

	producer := mq.NewProducer()
	if producer != nil {
		relay := outbox.NewRelay(db, producer)
		relay.Start()
		defer relay.Stop()
	}

	shutdown, serverOpt, _ := obs.InitGRPCProvider("collection")
	defer shutdown(context.Background())

	_ = obs.InitServiceMeters("collection")

	if err := grpcserver.StartServer(grpcserver.ServerConfig{
		ServiceName: "logos.collection",
		Port:        cfg.Ports.Collection,
		Etcd:        grpcserver.EtcdConfig{Endpoints: cfg.Etcd.Endpoints},
	}, func(s *grpc.Server) {
		pb.RegisterDataCollectionServiceServer(s, &handler.DataCollectionServiceImpl{
			CollectionService: collectionService,
		})
	}, serverOpt); err != nil {
		log.Fatalf("Collection service failed to run: %v", err)
	}
}
