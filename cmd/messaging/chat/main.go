package main

import (
	"Logos/config"
	"Logos/internal/service/messaging/chat/dao"
	"Logos/internal/service/messaging/chat/handler"
	"Logos/internal/service/messaging/chat/model"
	"Logos/internal/service/messaging/chat/service"
	"Logos/internal/service/messaging/types"
	"Logos/pkg/client"
	"Logos/pkg/database/pgsql"
	"Logos/pkg/grpcserver"
	"Logos/pkg/logger"
	"Logos/pkg/obs"
	pb "Logos/proto_gen/chat"
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

	eventBus := types.GetEventBus()

	chatRepo := dao.NewChatRepository(db)

	var botClient service.BotClient
	botRawClient, botErr := client.NewBotClientFromConfig(cfg)
	if botErr != nil {
		logger.Warn("连接Bot服务失败，@Bot功能不可用", logger.ErrorField(botErr))
	} else {
		botClient = service.NewBotClientAdapter(botRawClient)
		logger.Info("Bot服务客户端已连接")
	}

	var moderationClient service.ModerationClient
	modRawClient, modErr := client.NewModerationClientFromConfig(cfg)
	if modErr != nil {
		logger.Warn("连接Moderation服务失败，内容审核不可用", logger.ErrorField(modErr))
	} else {
		moderationClient = service.NewModerationClientAdapter(modRawClient)
		logger.Info("Moderation服务客户端已连接")
	}

	chatService := service.NewChatServiceWithAI(chatRepo, eventBus, ctx, botClient, moderationClient)
	chatServiceImpl := handler.NewChatServiceImpl(chatService)

	if err := chatService.StartEventConsumer(); err != nil {
		log.Fatalf("Failed to start event consumer: %v", err)
	}
	logger.Info("聊天服务事件消费者已启动")

	shutdown, serverOpt, _ := obs.InitGRPCProvider("chat")
	defer shutdown(context.Background())

	if err := grpcserver.StartServer(grpcserver.ServerConfig{
		ServiceName: "logos.chat",
		Port:        cfg.Ports.Chat,
		Etcd:        grpcserver.EtcdConfig{Endpoints: cfg.Etcd.Endpoints},
	}, func(s *grpc.Server) {
		pb.RegisterChatServiceServer(s, chatServiceImpl)
	}, serverOpt); err != nil {
		log.Fatalf("Chat service failed to run: %v", err)
	}
}
