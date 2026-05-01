package main

import (
	"context"
	"log"

	"Logos/config"
	"Logos/internal/service/platform/billing/dao"
	"Logos/internal/service/platform/billing/handler"
	"Logos/internal/service/platform/billing/model"
	"Logos/internal/service/platform/billing/service"
	"Logos/pkg/database/pgsql"
	"Logos/pkg/grpcserver"
	"Logos/pkg/logger"
	"Logos/pkg/mq"
	"Logos/pkg/obs"
	pb "Logos/proto_gen/billing"

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

	repo := dao.NewBillingRepository(db)

	var producer *mq.Producer
	if len(cfg.Kafka.Brokers) > 0 {
		producer = mq.NewProducer()
	}

	billingService := service.NewBillingService(repo, producer)

	shutdown, serverOpt, _ := obs.InitGRPCProvider("billing")
	defer shutdown(context.Background())

	if err := grpcserver.StartServer(grpcserver.ServerConfig{
		ServiceName: "logos.billing",
		Port:        cfg.Ports.Billing,
		Etcd:        grpcserver.EtcdConfig{Endpoints: cfg.Etcd.Endpoints},
	}, func(s *grpc.Server) {
		pb.RegisterBillingServiceServer(s, &handler.BillingServiceImpl{
			BillingService: billingService,
		})
	}, serverOpt); err != nil {
		log.Fatalf("Billing service failed to run: %v", err)
	}
}
