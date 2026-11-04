package main

import (
	"context"
	"fmt"
	"log"

	"Logos/config"
	"Logos/internal/service/ai/vector/dao"
	"Logos/internal/service/ai/vector/handler"
	"Logos/internal/service/ai/vector/model"
	"Logos/internal/service/ai/vector/service"
	pgsql "Logos/pkg/database/pgsql"
	"Logos/pkg/eino"
	"Logos/pkg/governance"
	"Logos/pkg/grpcserver"
	"Logos/pkg/health"
	"Logos/pkg/logger"
	"Logos/pkg/obs"
	"Logos/pkg/vector"
	pb "Logos/proto_gen/vector"

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

	_ = obs.InitServiceMeters("vector")

	// Milvus 是向量服务的核心依赖：连不上时查询会全部失败，
	// 不应该让实例继续接检索流量。Eino 只在写入时需要，作为可选检查。
	ready := health.ServiceRegistry(db)
	if milvusManager != nil {
		ready.Register("milvus", func(ctx context.Context) error {
			return milvusManager.Health(ctx)
		})
	} else {
		ready.Register("milvus", func(context.Context) error {
			return fmt.Errorf("milvus: not initialized")
		})
	}

	if err := grpcserver.StartServer(grpcserver.ServerConfig{
		ServiceName: "logos.vector",
		Port:        cfg.Ports.Vector,
		Etcd:        grpcserver.EtcdConfig{Endpoints: cfg.Etcd.Endpoints},
		Governance:  governance.DefaultConfig(),
		ReadyCheck:  ready,
	}, func(s *grpc.Server) {
		pb.RegisterVectorServiceServer(s, &handler.VectorServiceImpl{
			VectorService: vectorService,
		})
	}, serverOpt); err != nil {
		log.Fatalf("Vector service failed to run: %v", err)
	}
}
