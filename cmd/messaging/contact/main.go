package main

import (
	"Logos/config"
	"Logos/internal/service/messaging/contact/dao"
	"Logos/internal/service/messaging/contact/handler"
	"Logos/internal/service/messaging/contact/model"
	"Logos/internal/service/messaging/contact/service"
	"Logos/pkg/database/pgsql"
	"Logos/pkg/governance"
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

	ctx := context.Background()

	// 初始化数据库
	db, err := pgsql.InitPostgres()
	if err != nil {
		log.Fatalf("Failed to init postgres: %v", err)
	}

	// 自动迁移数据库
	if err := model.AutoMigrate(db); err != nil {
		log.Fatalf("Failed to auto migrate: %v", err)
	}

	// 初始化各层
	contactRepo := dao.NewContactRepository(db)
	contactService := service.NewContactService(contactRepo, ctx)
	contactServiceImpl := handler.NewContactServiceImpl(contactService)

	// 初始化可观测性
	shutdown, serverOpt, _ := obs.InitGRPCProvider("contact")
	defer shutdown(context.Background())

	// 启动服务
	if err := grpcserver.StartServer(grpcserver.ServerConfig{
		ServiceName: "logos.contact",
		Port:        cfg.Ports.Contact,
		Etcd:        grpcserver.EtcdConfig{Endpoints: cfg.Etcd.Endpoints},
		Governance:  governance.DefaultConfig(),
	}, func(s *grpc.Server) {
		pb.RegisterContactServiceServer(s, contactServiceImpl)
	}, serverOpt); err != nil {
		log.Fatalf("Contact service failed to run: %v", err)
	}
}
