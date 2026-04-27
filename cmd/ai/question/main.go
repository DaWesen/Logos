package main

import (
	"Logos/config"
	"Logos/internal/service/ai/question/dao"
	"Logos/internal/service/ai/question/handler"
	"Logos/internal/service/ai/question/model"
	"Logos/internal/service/ai/question/service"
	"Logos/pkg/database/pgsql"
	"Logos/pkg/eino"
	"Logos/pkg/grpcserver"
	"Logos/pkg/logger"
	"Logos/pkg/obs"
	pb "Logos/proto_gen/question"
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

	repo := dao.NewQARepository(db)

	qaService := service.NewQAService(repo, einoClient, nil, nil, nil)

	shutdown, serverOpt, _ := obs.InitGRPCProvider("question")
	defer shutdown(context.Background())

	if err := grpcserver.StartServer(grpcserver.ServerConfig{
		ServiceName: "logos.question",
		Port:        cfg.Ports.Question,
		Etcd:        grpcserver.EtcdConfig{Endpoints: cfg.Etcd.Endpoints},
	}, func(s *grpc.Server) {
		pb.RegisterQAServiceServer(s, &handler.QAServiceImpl{
			QAService: qaService,
		})
	}, serverOpt); err != nil {
		log.Fatalf("Question service failed to run: %v", err)
	}
}
