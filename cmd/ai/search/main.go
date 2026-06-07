package main

import (
	"Logos/config"
	"Logos/internal/service/ai/search/dao"
	"Logos/internal/service/ai/search/handler"
	"Logos/internal/service/ai/search/service"
	"Logos/pkg/es"
	"Logos/pkg/grpcserver"
	"Logos/pkg/logger"
	"Logos/pkg/obs"
	pb "Logos/proto_gen/search"
	"context"
	"log"

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

	if err := grpcserver.StartServer(grpcserver.ServerConfig{
		ServiceName: "logos.search",
		Port:        cfg.Ports.Search,
		Etcd:        grpcserver.EtcdConfig{Endpoints: cfg.Etcd.Endpoints},
	}, func(s *grpc.Server) {
		pb.RegisterSearchServiceServer(s, &handler.SearchServiceImpl{
			SearchService: searchService,
		})
	}, serverOpt); err != nil {
		log.Fatalf("Search service failed to run: %v", err)
	}
}
