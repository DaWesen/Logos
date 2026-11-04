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
	"Logos/pkg/health"
	"Logos/pkg/httpserver"
	"Logos/pkg/logger"
	"Logos/pkg/obs"
	pb "Logos/proto_gen/monitoring"
	"context"
	"fmt"
	"log"
	"net"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"
)

// monitoringHTTPShutdownTimeout 是看板排空在途请求的预算。
// 主流程等待时间必须不小于它，否则 Shutdown 还没结束进程就先退出了。
const monitoringHTTPShutdownTimeout = 15 * time.Second

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

	// HTTP 看板与 gRPC 服务共用同一个信号生命周期：SIGTERM 时先停止接收新请求、
	// 再等在途请求完成，而不是直接切断。
	sigCtx, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()

	httpHandler := monitoringhttp.NewMonitoringHTTPHandler()
	httpDone := startHTTPServer(sigCtx, cfg.Ports.Monitoring+10000, httpHandler)

	shutdown, serverOpt, _ := obs.InitGRPCProvider("monitoring")
	defer shutdown(context.Background())

	_ = obs.InitServiceMeters("monitoring")

	// 保留策略与采集必须成对存在：只写不删，监控库最终会被自己的数据撑爆。
	// 放在观测 Provider 初始化之后启动，确保首轮清理的删除量能上报。
	retention := collector.NewRetentionCleaner(db, collector.RetentionPolicyFromEnv())
	retentionCtx, retentionCancel := context.WithCancel(context.Background())
	defer retentionCancel()
	go retention.Start(retentionCtx)
	log.Println("Monitoring retention cleaner started")

	ready := health.ServiceRegistry(db)

	serveErr := grpcserver.StartServer(grpcserver.ServerConfig{
		ServiceName: "logos.monitoring",
		Port:        cfg.Ports.Monitoring,
		Etcd:        grpcserver.EtcdConfig{Endpoints: cfg.Etcd.Endpoints},
		ReadyCheck:  ready,
	}, func(s *grpc.Server) {
		pb.RegisterMonitoringServiceServer(s, &grpchandler.MonitoringServiceImpl{
			MonitoringService: monitoringService,
		})
	}, serverOpt)

	// gRPC 已退出，通知 HTTP 侧进入优雅停机并等待其排空。
	stopSignals()
	select {
	case <-httpDone:
	case <-time.After(monitoringHTTPShutdownTimeout + time.Second):
		log.Println("Monitoring HTTP shutdown timed out")
	}

	if serveErr != nil {
		log.Fatalf("Monitoring service failed to run: %v", serveErr)
	}
}

// startHTTPServer 启动监控看板。
//
// 之前用裸 http.ListenAndServe：没有 ReadHeaderTimeout（慢请求头可长期占住连接）、
// 没有优雅停机（SIGTERM 直接切断在途请求），而且 Serve 出错时 log.Fatalf 会把
// 整个进程杀掉——端口被占用不该连带 gRPC 服务一起退出。
// 现在监听失败只记录错误并返回已关闭的 done channel，gRPC 服务继续提供能力。
func startHTTPServer(ctx context.Context, port int, h *monitoringhttp.MonitoringHTTPHandler) <-chan struct{} {
	done := make(chan struct{})

	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		logger.Error("Monitoring HTTP listen failed, dashboard disabled",
			logger.IntField("port", port),
			logger.ErrorField(err))
		close(done)
		return done
	}

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	h.RegisterRoutes(r)

	srv := httpserver.New(ln.Addr().String(), r, httpserver.Options{})
	log.Printf("Monitoring HTTP server starting on %s", ln.Addr().String())

	go func() {
		defer close(done)
		if err := httpserver.Serve(ctx, srv, ln, monitoringHTTPShutdownTimeout); err != nil {
			logger.Error("Monitoring HTTP server stopped with error", logger.ErrorField(err))
		}
	}()
	return done
}
