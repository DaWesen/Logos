package main

import (
	"Logos/config"
	"Logos/internal/ai/recommend/dao"
	"Logos/internal/ai/recommend/handler"
	"Logos/internal/ai/recommend/model"
	"Logos/internal/ai/recommend/service"
	"Logos/pkg/database/pgsql"
	"Logos/pkg/eino"
	"Logos/pkg/grpcserver"
	"Logos/pkg/logger"
	"Logos/pkg/obs"
	pb "Logos/proto_gen/recommend"
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

	repo := dao.NewRecommendRepository(db)

	recommendService := service.NewRecommendService(repo, einoClient, nil, nil)

	shutdown, serverOpt, _ := obs.InitGRPCProvider("recommend")
	defer shutdown(context.Background())

	if err := grpcserver.StartServer(grpcserver.ServerConfig{
		ServiceName: "logos.recommend",
		Port:        cfg.Ports.Recommend,
		Etcd:        grpcserver.EtcdConfig{Endpoints: cfg.Etcd.Endpoints},
	}, func(s *grpc.Server) {
		pb.RegisterRecommendationServiceServer(s, &handler.RecommendationServiceImpl{
			RecommendService: recommendService,
		})
	}, serverOpt); err != nil {
		log.Fatalf("Recommend service failed to run: %v", err)
	}
}
