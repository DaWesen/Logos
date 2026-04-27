package main

import (
	"Logos/config"
	"Logos/internal/service/messaging/message/dao"
	"Logos/internal/service/messaging/message/handler"
	"Logos/internal/service/messaging/message/model"
	"Logos/internal/service/messaging/message/service"
	"Logos/pkg/database/pgsql"
	"Logos/pkg/grpcserver"
	"Logos/pkg/logger"
	"Logos/pkg/obs"
	pb "Logos/proto_gen/message"
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

	repo := dao.NewMessageRepository(db)

	messageService := service.NewMessageService(repo, nil)

	shutdown, serverOpt, _ := obs.InitGRPCProvider("message")
	defer shutdown(context.Background())

	if err := grpcserver.StartServer(grpcserver.ServerConfig{
		ServiceName: "logos.message",
		Port:        cfg.Ports.Message,
		Etcd:        grpcserver.EtcdConfig{Endpoints: cfg.Etcd.Endpoints},
	}, func(s *grpc.Server) {
		pb.RegisterMessageServiceServer(s, &handler.MessageServiceImpl{
			MessageService: messageService,
		})
	}, serverOpt); err != nil {
		log.Fatalf("Message service failed to run: %v", err)
	}
}
