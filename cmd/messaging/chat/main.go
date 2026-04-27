package main

import (
	"Logos/config"
	"Logos/internal/messaging/chat/handler"
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

	shutdown, serverOpt, _ := obs.InitGRPCProvider("chat")
	defer shutdown(context.Background())

	if err := grpcserver.StartServer(grpcserver.ServerConfig{
		ServiceName: "logos.chat",
		Port:        cfg.Ports.Chat,
		Etcd:        grpcserver.EtcdConfig{Endpoints: cfg.Etcd.Endpoints},
	}, func(s *grpc.Server) {
		pb.RegisterChatServiceServer(s, &handler.ChatServiceImpl{})
	}, serverOpt); err != nil {
		log.Fatalf("Chat service failed to run: %v", err)
	}
}
