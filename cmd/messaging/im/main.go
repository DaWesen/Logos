package main

import (
	"Logos/config"
	"Logos/internal/service/messaging/im/dao"
	"Logos/internal/service/messaging/im/handler"
	"Logos/internal/service/messaging/im/model"
	"Logos/internal/service/messaging/im/service"
	"Logos/pkg/cache"
	"Logos/pkg/database/pgsql"
	"Logos/pkg/grpcserver"
	"Logos/pkg/logger"
	"Logos/pkg/obs"
	pb "Logos/proto_gen/im"
	"context"
	"log"

	"google.golang.org/grpc"
)

func main() {
	cfg := config.GetConfig()

	logger.InitLogger()

	ctx := context.Background()
	db, err := pgsql.InitPostgres()
	if err != nil {
		log.Fatalf("Failed to init postgres: %v", err)
	}

	if err := model.AutoMigrate(db); err != nil {
		log.Fatalf("Failed to auto migrate: %v", err)
	}

	repo := dao.NewIMRepository(db)
	redisCache := cache.NewRedisCache()
	imService := service.NewIMService(repo, redisCache, ctx)
	imServiceImpl := handler.NewIMServiceImpl(imService)

	shutdown, serverOpt, _ := obs.InitGRPCProvider("im")
	defer shutdown(context.Background())

	if err := grpcserver.StartServer(grpcserver.ServerConfig{
		ServiceName: "logos.im",
		Port:        cfg.Ports.IM,
		Etcd:        grpcserver.EtcdConfig{Endpoints: cfg.Etcd.Endpoints},
	}, func(s *grpc.Server) {
		pb.RegisterIMServiceServer(s, imServiceImpl)
	}, serverOpt); err != nil {
		log.Fatalf("IM service failed to run: %v", err)
	}
}
