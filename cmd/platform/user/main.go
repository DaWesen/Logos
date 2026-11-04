package main

import (
	"Logos/config"
	"Logos/internal/service/platform/user/dao"
	"Logos/internal/service/platform/user/handler"
	"Logos/internal/service/platform/user/model"
	"Logos/internal/service/platform/user/service"
	"Logos/pkg/cache"
	"Logos/pkg/database/pgsql"
	"Logos/pkg/es"
	"Logos/pkg/grpcserver"
	"Logos/pkg/health"
	"Logos/pkg/jwt"
	"Logos/pkg/logger"
	"Logos/pkg/mq"
	"Logos/pkg/obs"
	"Logos/pkg/outbox"
	pb "Logos/proto_gen/user"
	"context"
	"log"
	"time"

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

	jwtManager := jwt.NewJWTManager()
	cacheInstance := cache.NewRedisCache()
	producer := mq.NewProducer()
	outboxRepo := outbox.NewOutboxRepository()

	esClient, err := es.InitElasticsearch()
	var esManager *es.ESManager
	if err == nil && esClient != nil {
		esManager = es.NewESManager(esClient)
	}

	repo := dao.NewUserRepository(db)

	userService := service.NewUserService(repo, jwtManager, cacheInstance, outboxRepo, esManager)

	if producer != nil {
		relay := outbox.NewRelay(db, producer)
		relay.Start()
		defer relay.Stop()
	}

	shutdown, serverOpt, _ := obs.InitGRPCProvider("user")
	defer shutdown(context.Background())

	_ = obs.InitServiceMeters("user")

	// 用户读写的唯一真相源是 PostgreSQL；Redis 只是缓存，
	// 不可用时查询会回源，不应该因此摘掉整个服务。
	ready := health.New(health.WithTimeout(3 * time.Second))
	ready.Register("postgres", health.GormPostgres(db))
	ready.RegisterOptional("redis", health.Redis(cacheInstance))

	if err := grpcserver.StartServer(grpcserver.ServerConfig{
		ServiceName: "logos.user",
		Port:        cfg.Ports.User,
		Etcd:        grpcserver.EtcdConfig{Endpoints: cfg.Etcd.Endpoints},
		ReadyCheck:  ready,
	}, func(s *grpc.Server) {
		pb.RegisterUserServiceServer(s, &handler.UserServiceImpl{
			UserService: userService,
		})
	}, serverOpt); err != nil {
		log.Fatalf("User service failed to run: %v", err)
	}
}
