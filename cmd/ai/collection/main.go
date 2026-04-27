package main

import (
	"Logos/config"
	"Logos/internal/ai/collection/dao"
	"Logos/internal/ai/collection/handler"
	"Logos/internal/ai/collection/model"
	"Logos/internal/ai/collection/service"
	"Logos/pkg/database/pgsql"
	"Logos/pkg/grpcserver"
	"Logos/pkg/logger"
	"Logos/pkg/mq"
	"Logos/pkg/obs"
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

	var kafkaManager *mq.KafkaManager
	kafkaManager, err = mq.NewKafkaManager(cfg.Kafka.Brokers)
	if err != nil {
		log.Printf("Failed to init kafka, will continue without kafka: %v", err)
	} else {
		_ = kafkaManager.CreateTopic(mq.TopicDataCollection)
		log.Println("Kafka initialized with data_collection topic")
	}

	repo := dao.NewCollectionRepository(db)

	var knowledgeService service.KnowledgeService
	var extractionService service.ExtractionService

	collectionService := service.NewCollectionService(repo, knowledgeService, extractionService)

	shutdown, serverOpt, _ := obs.InitGRPCProvider("collection")
	defer shutdown(context.Background())

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
