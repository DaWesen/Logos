package main

import (
	"Logos/config"
	"Logos/internal/messaging/im/handler"
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

	shutdown, serverOpt, _ := obs.InitGRPCProvider("im")
	defer shutdown(context.Background())

	if err := grpcserver.StartServer(grpcserver.ServerConfig{
		ServiceName: "logos.im",
		Port:        cfg.Ports.IM,
		Etcd:        grpcserver.EtcdConfig{Endpoints: cfg.Etcd.Endpoints},
	}, func(s *grpc.Server) {
		pb.RegisterIMServiceServer(s, &handler.IMServiceImpl{})
	}, serverOpt); err != nil {
		log.Fatalf("IM service failed to run: %v", err)
	}
}
