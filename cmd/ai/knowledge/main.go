package main

import (
	"Logos/config"
	"Logos/internal/service/ai/knowledge/consumer"
	"Logos/internal/service/ai/knowledge/dao"
	"Logos/internal/service/ai/knowledge/handler"
	"Logos/internal/service/ai/knowledge/model"
	"Logos/internal/service/ai/knowledge/service"
	"Logos/pkg/cache"
	"Logos/pkg/database/pgsql"
	"Logos/pkg/es"
	"Logos/pkg/graph"
	"Logos/pkg/grpcserver"
	"Logos/pkg/logger"
	"Logos/pkg/mq"
	"Logos/pkg/obs"
	pb "Logos/proto_gen/knowledge"
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

	kafkaManager, err := mq.NewKafkaManager(cfg.Kafka.Brokers)
	if err != nil {
		log.Printf("Failed to init kafka manager: %v", err)
	}

	var producer *mq.Producer
	if kafkaManager != nil {
		producer = mq.NewProducer()
	}

	cacheInstance := cache.NewRedisCache()

	var esManager *es.ESManager
	esClient, err := es.InitElasticsearch()
	if err != nil {
		log.Printf("Failed to init elasticsearch: %v", err)
	}
	if esClient != nil {
		esManager = es.NewESManager(esClient)
	}

	var neo4jManager *graph.Neo4jManager
	neo4jDriver, err := graph.InitNeo4j()
	if err != nil {
		log.Printf("Failed to init neo4j: %v", err)
	}
	if neo4jDriver != nil {
		neo4jManager = graph.NewNeo4jManager(neo4jDriver)
	}

	repo := dao.NewKnowledgeRepository(db, neo4jManager)

	knowledgeService := service.NewKnowledgeService(repo, cacheInstance, producer, esManager)

	if kafkaManager != nil && esManager != nil {
		esConsumer := mq.NewConsumer("knowledge_events", "knowledge-es-consumer")
		esSyncer := consumer.NewESConsumer(esConsumer, esManager)
		go func() {
			if err := esSyncer.Start(context.Background()); err != nil {
				logger.Warn("ES消费者启动失败", logger.ErrorField(err))
			}
		}()
		logger.Info("ES同步消费者已启动")
	}

	shutdown, serverOpt, _ := obs.InitGRPCProvider("knowledge")
	defer shutdown(context.Background())

	if err := grpcserver.StartServer(grpcserver.ServerConfig{
		ServiceName: "logos.knowledge",
		Port:        cfg.Ports.Knowledge,
		Etcd:        grpcserver.EtcdConfig{Endpoints: cfg.Etcd.Endpoints},
	}, func(s *grpc.Server) {
		pb.RegisterKnowledgeServiceServer(s, &handler.KnowledgeServiceImpl{
			KnowledgeService: knowledgeService,
		})
	}, serverOpt); err != nil {
		log.Fatalf("Knowledge service failed to run: %v", err)
	}
}
