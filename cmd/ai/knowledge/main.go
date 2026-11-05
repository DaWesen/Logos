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
	"Logos/pkg/health"
	"Logos/pkg/logger"
	"Logos/pkg/mq"
	"Logos/pkg/obs"
	"Logos/pkg/outbox"
	pb "Logos/proto_gen/knowledge"
	"context"
	"log"
	"os/signal"
	"syscall"

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

	outboxRepo := outbox.NewOutboxRepository()

	knowledgeService := service.NewKnowledgeService(repo, cacheInstance, outboxRepo, esManager)

	if producer != nil {
		relay := outbox.NewRelay(db, producer)
		relay.Start()
		defer relay.Stop()
	}

	// 消费循环必须跟随进程生命周期：否则 SIGTERM 后 gRPC 已经排空退出，
	// 后台消费者仍在拉取消息，handler 收到的 msg.Context 也永远不会取消。
	consumerCtx, stopConsumers := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopConsumers()

	if kafkaManager != nil && esManager != nil {
		esConsumer := mq.NewConsumer("knowledge_events", "knowledge-es-consumer")
		esSyncer := consumer.NewESConsumer(esConsumer, esManager)
		// Subscribe 内部已经起 goroutine 跑消费循环，不会阻塞，这里无需再包
		// 一层 goroutine：包了之后启动错误只会进日志，主流程照样打印「已启动」。
		if err := esSyncer.Start(consumerCtx); err != nil {
			logger.Warn("ES消费者启动失败", logger.ErrorField(err))
		} else {
			logger.Info("ES同步消费者已启动")
		}
	}

	shutdown, serverOpt, _ := obs.InitGRPCProvider("knowledge")
	defer shutdown(context.Background())

	_ = obs.InitServiceMeters("knowledge")

	ready := health.ServiceRegistry(db, health.Redis(cacheInstance))

	if err := grpcserver.StartServer(grpcserver.ServerConfig{
		ServiceName: "logos.knowledge",
		Port:        cfg.Ports.Knowledge,
		Etcd:        grpcserver.EtcdConfig{Endpoints: cfg.Etcd.Endpoints},
		ReadyCheck:  ready,
	}, func(s *grpc.Server) {
		pb.RegisterKnowledgeServiceServer(s, &handler.KnowledgeServiceImpl{
			KnowledgeService: knowledgeService,
		})
	}, serverOpt); err != nil {
		log.Fatalf("Knowledge service failed to run: %v", err)
	}
}
