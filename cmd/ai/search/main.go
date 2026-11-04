package main

import (
	"Logos/config"
	"Logos/internal/service/ai/search/dao"
	"Logos/internal/service/ai/search/handler"
	"Logos/internal/service/ai/search/service"
	"Logos/pkg/es"
	"Logos/pkg/grpcserver"
	"Logos/pkg/health"
	"Logos/pkg/logger"
	"Logos/pkg/obs"
	pb "Logos/proto_gen/search"
	"context"
	"fmt"
	"log"
	"time"

	"google.golang.org/grpc"
)

func main() {
	cfg := config.GetConfig()

	logger.InitLogger()

	var esManager *es.ESManager
	esClient, err := es.InitElasticsearch()
	if err != nil {
		log.Printf("Failed to init elasticsearch: %v", err)
	}
	if esClient != nil {
		esManager = es.NewESManager(esClient)
	}

	repo := dao.NewSearchRepository(esManager)

	searchService := service.NewSearchService(repo)

	shutdown, serverOpt, _ := obs.InitGRPCProvider("search")
	defer shutdown(context.Background())

	_ = obs.InitServiceMeters("search")

	// ES 是 Search 的唯一数据源：连不上时所有检索都会失败，
	// 必须作为硬依赖摘流量；esManager 为空说明启动时就没连上。
	ready := health.New(health.WithTimeout(3 * time.Second))
	if esManager != nil {
		ready.Register("elasticsearch", func(ctx context.Context) error {
			return esManager.Health(ctx)
		})
	} else {
		ready.Register("elasticsearch", func(context.Context) error {
			return fmt.Errorf("elasticsearch: not initialized")
		})
	}

	if err := grpcserver.StartServer(grpcserver.ServerConfig{
		ServiceName: "logos.search",
		Port:        cfg.Ports.Search,
		Etcd:        grpcserver.EtcdConfig{Endpoints: cfg.Etcd.Endpoints},
		ReadyCheck:  ready,
	}, func(s *grpc.Server) {
		pb.RegisterSearchServiceServer(s, &handler.SearchServiceImpl{
			SearchService: searchService,
		})
	}, serverOpt); err != nil {
		log.Fatalf("Search service failed to run: %v", err)
	}
}
