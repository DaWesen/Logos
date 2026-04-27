package main

import (
	"Logos/config"
	"Logos/internal/service/messaging/contact/handler"
	"Logos/pkg/grpcserver"
	"Logos/pkg/logger"
	"Logos/pkg/obs"
	pb "Logos/proto_gen/contact"
	"context"
	"log"

	"google.golang.org/grpc"
)

func main() {
	cfg := config.GetConfig()

	logger.InitLogger()

	shutdown, serverOpt, _ := obs.InitGRPCProvider("contact")
	defer shutdown(context.Background())

	if err := grpcserver.StartServer(grpcserver.ServerConfig{
		ServiceName: "logos.contact",
		Port:        cfg.Ports.Contact,
		Etcd:        grpcserver.EtcdConfig{Endpoints: cfg.Etcd.Endpoints},
	}, func(s *grpc.Server) {
		pb.RegisterContactServiceServer(s, &handler.ContactServiceImpl{})
	}, serverOpt); err != nil {
		log.Fatalf("Contact service failed to run: %v", err)
	}
}
