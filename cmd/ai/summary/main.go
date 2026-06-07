package main

import (
	"context"
	"log"

	"Logos/config"
	"Logos/internal/service/ai/summary/dao"
	"Logos/internal/service/ai/summary/handler"
	"Logos/internal/service/ai/summary/model"
	"Logos/internal/service/ai/summary/service"
	"Logos/pkg/client"
	"Logos/pkg/database/pgsql"
	"Logos/pkg/eino"
	"Logos/pkg/grpcserver"
	"Logos/pkg/logger"
	"Logos/pkg/obs"
	pb "Logos/proto_gen/summary"

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

	repo := dao.NewSummaryRepository(db)

	var chatSvc service.ChatService
	chatClient, chatErr := client.NewChatClientFromConfig(cfg)
	if chatErr != nil {
		logger.Warn("初始化Chat客户端失败，摘要服务将降级运行", logger.ErrorField(chatErr))
	} else {
		chatSvc = service.NewChatClientAdapter(chatClient)
	}

	summaryService := service.NewSummaryService(repo, einoClient, chatSvc)

	shutdown, serverOpt, _ := obs.InitGRPCProvider("summary")
	defer shutdown(context.Background())

	_ = obs.InitServiceMeters("summary")

	if err := grpcserver.StartServer(grpcserver.ServerConfig{
		ServiceName: "logos.summary",
		Port:        cfg.Ports.Summary,
		Etcd:        grpcserver.EtcdConfig{Endpoints: cfg.Etcd.Endpoints},
	}, func(s *grpc.Server) {
		pb.RegisterSummaryServiceServer(s, &handler.SummaryServiceImpl{
			SummaryService: summaryService,
		})
	}, serverOpt); err != nil {
		log.Fatalf("Summary service failed to run: %v", err)
	}
}
