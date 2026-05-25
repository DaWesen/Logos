package main

import (
	"Logos/config"
	"Logos/internal/service/platform/monitoring/collector"
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
	"time"

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

	if len(cfg.Etcd.Endpoints) > 0 {
		svcCollector, err := collector.NewServiceCollector(db, cfg.Etcd.Endpoints, 30*time.Second)
		if err != nil {
			log.Printf("Warning: Failed to create service collector: %v", err)
		} else {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go svcCollector.Start(ctx)
			defer svcCollector.Close()
			log.Println("Service collector started")
		}
	} else {
		log.Println("No etcd endpoints configured, service collector not started")
	}

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
