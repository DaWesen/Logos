package main

import (
	"Logos/config"
	"Logos/internal/service/platform/monitoring/dao"
	"Logos/internal/service/platform/monitoring/handler"
	"Logos/internal/service/platform/monitoring/model"
	"Logos/internal/service/platform/monitoring/service"
	"Logos/pkg/database/pgsql"
	"Logos/pkg/grpcserver"
	"Logos/pkg/logger"
	"Logos/pkg/obs"
	pb "Logos/proto_gen/monitoring"
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

	if err := model.AutoMigrate(db); err != nil {
		log.Fatalf("Failed to auto migrate: %v", err)
	}

	repo := dao.NewMonitoringRepository(db)

	monitoringService := service.NewMonitoringService(repo)

	shutdown, serverOpt, _ := obs.InitGRPCProvider("monitoring")
	defer shutdown(context.Background())

	if err := grpcserver.StartServer(grpcserver.ServerConfig{
		ServiceName: "logos.monitoring",
		Port:        cfg.Ports.Monitoring,
		Etcd:        grpcserver.EtcdConfig{Endpoints: cfg.Etcd.Endpoints},
	}, func(s *grpc.Server) {
		pb.RegisterMonitoringServiceServer(s, &handler.MonitoringServiceImpl{
			MonitoringService: monitoringService,
		})
	}, serverOpt); err != nil {
		log.Fatalf("Monitoring service failed to run: %v", err)
	}
}
