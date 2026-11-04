package main

import (
	"context"
	"log"
	"time"

	"Logos/config"
	"Logos/internal/service/platform/billing/dao"
	"Logos/internal/service/platform/billing/handler"
	"Logos/internal/service/platform/billing/model"
	"Logos/internal/service/platform/billing/service"
	"Logos/pkg/database/pgsql"
	"Logos/pkg/grpcserver"
	"Logos/pkg/health"
	"Logos/pkg/logger"
	"Logos/pkg/mq"
	"Logos/pkg/obs"
	"Logos/pkg/outbox"
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

	if err := outbox.AutoMigrate(db); err != nil {
		log.Fatalf("Failed to auto migrate outbox: %v", err)
	}

	repo := dao.NewBillingRepository(db)
	outboxRepo := outbox.NewOutboxRepository()

	var producer *mq.Producer
	if len(cfg.Kafka.Brokers) > 0 {
		producer = mq.NewProducer()
	}

	billingService := service.NewBillingService(repo, outboxRepo, cfg.Payment.CallbackSecret,
		service.WithPaymentMethods(cfg.Payment.AllowedMethods),
		service.WithPaymentAmountLimits(cfg.Payment.MinAmount, cfg.Payment.MaxAmount),
	)

	// 后台兜底回收过期支付单（按需回收只覆盖被访问过的订单）
	reaper := service.NewPaymentOrderReaper(repo)
	reaper.Start()
	defer reaper.Stop()

	if producer != nil {
		relay := outbox.NewRelay(db, producer)
		relay.Start()
		defer relay.Stop()
	}

	shutdown, serverOpt, _ := obs.InitGRPCProvider("billing")
	defer shutdown(context.Background())

	_ = obs.InitServiceMeters("billing")

	// 余额与交易记录都落 PostgreSQL；数据库不可用时不能继续接扣费请求，
	// 否则用户会看到「请求超时但可能已扣款」的不确定状态。
	ready := health.New(health.WithTimeout(3 * time.Second))
	ready.Register("postgres", health.GormPostgres(db))

	if err := grpcserver.StartServer(grpcserver.ServerConfig{
		ServiceName: "logos.billing",
		Port:        cfg.Ports.Billing,
		Etcd:        grpcserver.EtcdConfig{Endpoints: cfg.Etcd.Endpoints},
		ReadyCheck:  ready,
	}, func(s *grpc.Server) {
		pb.RegisterBillingServiceServer(s, &handler.BillingServiceImpl{
			BillingService: billingService,
		})
	}, serverOpt); err != nil {
		log.Fatalf("Billing service failed to run: %v", err)
	}
}
