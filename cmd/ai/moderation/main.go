package main

import (
	"context"
	"log"

	"Logos/config"
	"Logos/internal/service/ai/moderation/dao"
	"Logos/internal/service/ai/moderation/handler"
	"Logos/internal/service/ai/moderation/model"
	"Logos/internal/service/ai/moderation/service"
	"Logos/pkg/database/pgsql"
	"Logos/pkg/eino"
	"Logos/pkg/grpcserver"
	"Logos/pkg/logger"
	"Logos/pkg/obs"
	pb "Logos/proto_gen/moderation"

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

	repo := dao.NewModerationRepository(db)

	moderationService := service.NewModerationService(repo, einoClient)

	shutdown, serverOpt, _ := obs.InitGRPCProvider("moderation")
	defer shutdown(context.Background())

	if err := grpcserver.StartServer(grpcserver.ServerConfig{
		ServiceName: "logos.moderation",
		Port:        cfg.Ports.Moderation,
		Etcd:        grpcserver.EtcdConfig{Endpoints: cfg.Etcd.Endpoints},
	}, func(s *grpc.Server) {
		pb.RegisterModerationServiceServer(s, &handler.ModerationServiceImpl{
			ModerationService: moderationService,
		})
	}, serverOpt); err != nil {
		log.Fatalf("Moderation service failed to run: %v", err)
	}
}
