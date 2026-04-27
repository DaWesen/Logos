package main

import (
	"Logos/config"
	"Logos/internal/ai/vector/dao"
	"Logos/internal/ai/vector/handler"
	"Logos/internal/ai/vector/service"
	"Logos/pkg/eino"
	"Logos/pkg/grpcserver"
	"Logos/pkg/logger"
	"Logos/pkg/obs"
	"Logos/pkg/vector"
	pb "Logos/proto_gen/vector"
	"context"
	"log"

	"google.golang.org/grpc"
)

func main() {
	cfg := config.GetConfig()

	logger.InitLogger()

	var milvusManager *vector.MilvusManager
	milvusManager, err := vector.InitMilvus()
	if err != nil {
		log.Printf("Failed to init milvus: %v", err)
	}

	var einoClient *eino.EinoManager
	einoClient, err = eino.InitEino()
	if err != nil {
		log.Printf("Failed to init eino: %v", err)
	}

	repo := dao.NewVectorRepository(milvusManager, einoClient)

	vectorService := service.NewVectorService(repo)

	shutdown, serverOpt, _ := obs.InitGRPCProvider("vector")
	defer shutdown(context.Background())

	if err := grpcserver.StartServer(grpcserver.ServerConfig{
		ServiceName: "logos.vector",
		Port:        cfg.Ports.Vector,
		Etcd:        grpcserver.EtcdConfig{Endpoints: cfg.Etcd.Endpoints},
	}, func(s *grpc.Server) {
		pb.RegisterVectorServiceServer(s, &handler.VectorServiceImpl{
			VectorService: vectorService,
		})
	}, serverOpt); err != nil {
		log.Fatalf("Vector service failed to run: %v", err)
	}
}
