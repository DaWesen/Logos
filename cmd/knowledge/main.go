package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"Noah/config"
	"Noah/internal/knowledge/consumer"
	"Noah/internal/knowledge/dao"
	"Noah/internal/knowledge/handler"
	"Noah/internal/knowledge/model"
	"Noah/internal/knowledge/service"
	knowledge "Noah/kitex_gen/knowledge/knowledgeservice"
	"Noah/pkg/cache"
	"Noah/pkg/database/pgsql"
	"Noah/pkg/es"
	"Noah/pkg/graph"
	"Noah/pkg/logger"
	"Noah/pkg/mq"
	"log"

	"github.com/cloudwego/kitex/pkg/rpcinfo"
	"github.com/cloudwego/kitex/server"
	etcd "github.com/kitex-contrib/registry-etcd"
)

func main() {
	cfg := config.GetConfig()

	logger.InitLogger()

	db, err := pgsql.InitPostgres()
	if err != nil {
		log.Fatalf("Failed to init postgres: %v", err)
	}

	if err := model.AutoMigrate(db); err != nil {
		log.Fatalf("Failed to migrate database: %v", err)
	}

	var neo4jManager *graph.Neo4jManager
	neo4jDriver, err := graph.InitNeo4j()
	if err != nil {
		log.Printf("Failed to init neo4j, will continue without graph database: %v", err)
	} else {
		neo4jManager = graph.NewNeo4jManager(neo4jDriver)
	}

	var redisCache cache.Cache
	redisInstance := cache.NewRedisCache()
	if redisInstance != nil {
		redisCache = redisInstance
	}

	var kafkaProducer *mq.Producer
	kafkaProducer = mq.NewProducer()

	var kafkaConsumer *mq.Consumer
	kafkaConsumer = mq.NewConsumer("knowledge_events", "knowledge_es_consumer_group")

	var esManager *es.ESManager
	esClient, err := es.InitElasticsearch()
	if err != nil {
		log.Printf("Failed to init elasticsearch, will continue without search: %v", err)
	} else {
		esManager = es.NewESManager(esClient)
	}

	knowledgeRepo := dao.NewKnowledgeRepository(db, neo4jManager)

	knowledgeService := service.NewKnowledgeService(knowledgeRepo, redisCache, kafkaProducer, esManager)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if kafkaConsumer != nil && esManager != nil {
		esConsumer := consumer.NewESConsumer(kafkaConsumer, esManager)
		go func() {
			if err := esConsumer.Start(ctx); err != nil {
				log.Printf("ES consumer stopped with error: %v", err)
			}
		}()
	}

	r, err := etcd.NewEtcdRegistry(cfg.Etcd.Endpoints)
	if err != nil {
		log.Fatalf("Failed to create etcd registry: %v", err)
	}

	svr := knowledge.NewServer(
		&handler.KnowledgeServiceImpl{KnowledgeService: knowledgeService},
		server.WithRegistry(r),
		server.WithServerBasicInfo(&rpcinfo.EndpointBasicInfo{ServiceName: "knowledge"}),
	)

	go func() {
		if err := svr.Run(); err != nil {
			log.Fatalf("Knowledge service failed to run: %v", err)
		}
	}()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	logger.Info("正在关闭服务...")
	cancel()

	if kafkaConsumer != nil {
		_ = kafkaConsumer.Close()
	}
	if kafkaProducer != nil {
		_ = kafkaProducer.Close()
	}
	if redisCache != nil {
		_ = redisCache.Close()
	}

	logger.Info("服务已关闭")
}
