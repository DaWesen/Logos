package main

import (
	"context"
	"log"

	"Logos/config"
	"Logos/internal/bot"
	"Logos/internal/service/ai/bot/dao"
	"Logos/internal/service/ai/bot/handler"
	"Logos/internal/service/ai/bot/model"
	"Logos/internal/service/ai/bot/service"
	"Logos/pkg/client"
	"Logos/pkg/database/pgsql"
	"Logos/pkg/eino"
	"Logos/pkg/grpcserver"
	"Logos/pkg/logger"
	"Logos/pkg/mq"
	"Logos/pkg/obs"
	pb "Logos/proto_gen/bot"

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

	if err := bot.Init(); err != nil {
		log.Printf("Failed to init bot module: %v", err)
	}

	agentManager := bot.GetAgentManager()
	einoManager := eino.GetEinoManager()

	var billingService service.BillingService
	billingClient, err := client.NewBillingClientFromConfig(cfg)
	if err != nil {
		log.Printf("Failed to init billing client: %v", err)
	} else {
		billingService = billingClient
	}

	var vectorService service.VectorService
	vectorClient, err := client.NewVectorClientFromConfig(cfg)
	if err != nil {
		log.Printf("Failed to init vector client: %v", err)
	} else {
		vectorService = vectorClient
	}

	var producer *mq.Producer
	if len(cfg.Kafka.Brokers) > 0 {
		producer = mq.NewProducer()
	}

	repo := dao.NewBotRepository(db)
	botService := service.NewBotService(repo, agentManager, einoManager, billingService, vectorService, producer)

	shutdown, serverOpt, _ := obs.InitGRPCProvider("bot")
	defer shutdown(context.Background())

	if err := grpcserver.StartServer(grpcserver.ServerConfig{
		ServiceName: "logos.bot",
		Port:        cfg.Ports.Bot,
		Etcd:        grpcserver.EtcdConfig{Endpoints: cfg.Etcd.Endpoints},
	}, func(s *grpc.Server) {
		pb.RegisterBotServiceServer(s, &handler.BotServiceImpl{
			BotService: botService,
		})
	}, serverOpt); err != nil {
		log.Fatalf("Bot service failed to run: %v", err)
	}
}
