package main

import (
	"Logos/config"
	"Logos/internal/service/platform/monitoring/collector"
	"Logos/internal/service/platform/monitoring/dao"
	grpchandler "Logos/internal/service/platform/monitoring/handler"
	monitoringhttp "Logos/internal/service/platform/monitoring/http"
	"Logos/internal/service/platform/monitoring/model"
	"Logos/internal/service/platform/monitoring/service"
	"Logos/pkg/database/pgsql"
	"Logos/pkg/grpcserver"
	"Logos/pkg/logger"
	"Logos/pkg/obs"
	pb "Logos/proto_gen/monitoring"
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
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

	sysCollector := collector.NewSystemCollector(db, 30*time.Second)
	sysCtx, sysCancel := context.WithCancel(context.Background())
	defer sysCancel()
	go sysCollector.Start(sysCtx)
	log.Println("System metrics collector started")

	httpHandler := monitoringhttp.NewMonitoringHTTPHandler()
	go startHTTPServer(cfg.Ports.Monitoring+10000, httpHandler)

	shutdown, serverOpt, _ := obs.InitGRPCProvider("monitoring")
	defer shutdown(context.Background())

	_ = obs.InitServiceMeters("monitoring")

	if err := grpcserver.StartServer(grpcserver.ServerConfig{
		ServiceName: "logos.monitoring",
		Port:        cfg.Ports.Monitoring,
		Etcd:        grpcserver.EtcdConfig{Endpoints: cfg.Etcd.Endpoints},
	}, func(s *grpc.Server) {
		pb.RegisterMonitoringServiceServer(s, &grpchandler.MonitoringServiceImpl{
			MonitoringService: monitoringService,
		})
	}, serverOpt); err != nil {
		log.Fatalf("Monitoring service failed to run: %v", err)
	}
}

func startHTTPServer(port int, h *monitoringhttp.MonitoringHTTPHandler) {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	h.RegisterRoutes(r)

	addr := fmt.Sprintf(":%d", port)
	log.Printf("Monitoring HTTP server starting on %s", addr)
	if err := http.ListenAndServe(addr, r); err != nil {
		log.Fatalf("Monitoring HTTP server failed: %v", err)
	}
}
