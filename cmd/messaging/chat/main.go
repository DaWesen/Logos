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
	"Logos/pkg/governance"
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

	var translationClient service.TranslationClient
	if modRawClient != nil {
		translationClient = service.NewTranslationClientAdapter(modRawClient)
		logger.Info("翻译服务客户端已连接")
	}

	var contactChecker service.ContactChecker
	contactRawClient, contactErr := client.NewContactClientFromConfig(cfg)
	if contactErr != nil {
		logger.Warn("连接Contact服务失败，好友关系检查不可用", logger.ErrorField(contactErr))
	} else {
		contactChecker = service.NewContactCheckerAdapter(contactRawClient)
		logger.Info("Contact服务客户端已连接")
	}

	var userClient *client.UserClient
	userRawClient, userErr := client.NewUserClientFromConfig(cfg)
	if userErr != nil {
		logger.Warn("连接User服务失败，获取用户信息不可用", logger.ErrorField(userErr))
	} else {
		userClient = userRawClient
		logger.Info("User服务客户端已连接")
	}

	chatService := service.NewChatServiceWithContact(chatRepo, eventBus, ctx, botClient, moderationClient, translationClient, contactChecker, userClient)
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
		Host:        "127.0.0.1",
		Etcd:        grpcserver.EtcdConfig{Endpoints: cfg.Etcd.Endpoints},
		Governance:  governance.DefaultConfig(),
	}, func(s *grpc.Server) {
		pb.RegisterChatServiceServer(s, chatServiceImpl)
	}, serverOpt); err != nil {
		log.Fatalf("Chat service failed to run: %v", err)
	}
}
