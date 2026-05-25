package main

import (
	"Logos/config"
	"Logos/internal/service/ai/vector/dao"
	"Logos/internal/service/ai/vector/handler"
	"Logos/internal/service/ai/vector/model"
	"Logos/internal/service/ai/vector/service"
	"Logos/pkg/eino"
	"Logos/pkg/grpcserver"
	"Logos/pkg/logger"
	"Logos/pkg/obs"
	pgsql "Logos/pkg/database/pgsql"
	"Logos/pkg/vector"
	pb "Logos/proto_gen/vector"
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

	if autoMigrateErr := model.AutoMigrate(db); autoMigrateErr != nil {
		log.Printf("Warning: AutoMigrate failed: %v", autoMigrateErr)
	}

	var milvusManager *vector.MilvusManager
	milvusManager, err = vector.InitMilvus()
	if err != nil {
		logger.Warn("Milvus不可用，向量服务以降级模式运行", logger.ErrorField(err))
	} else {
		logger.Info("Milvus连接成功")
	}

	var einoClient *eino.EinoManager
	einoClient, err = eino.InitEino()
	if err != nil {
		log.Printf("Failed to init eino: %v", err)
	}

	repo := dao.NewVectorRepository(db, milvusManager, einoClient)

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
