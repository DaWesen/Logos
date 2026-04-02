package main

import (
	"log"

	"Noah/config"
	"Noah/internal/search/dao"
	"Noah/internal/search/handler"
	"Noah/internal/search/service"
	search "Noah/kitex_gen/search/searchservice"
	"Noah/pkg/es"
	"Noah/pkg/logger"

	"github.com/cloudwego/kitex/pkg/rpcinfo"
	"github.com/cloudwego/kitex/server"
	etcd "github.com/kitex-contrib/registry-etcd"
)

func main() {
	cfg := config.GetConfig()

	logger.InitLogger()

	esClient, err := es.InitElasticsearch()
	if err != nil {
		log.Fatalf("Failed to init elasticsearch: %v", err)
	}
	esManager := es.NewESManager(esClient)

	searchRepo := dao.NewSearchRepository(esManager)
	searchService := service.NewSearchService(searchRepo)

	r, err := etcd.NewEtcdRegistry(cfg.Etcd.Endpoints)
	if err != nil {
		log.Fatalf("Failed to create etcd registry: %v", err)
	}

	svr := search.NewServer(
		&handler.SearchServiceImpl{SearchService: searchService},
		server.WithRegistry(r),
		server.WithServerBasicInfo(&rpcinfo.EndpointBasicInfo{ServiceName: "search"}),
	)

	log.Printf("Search service starting...")
	if err := svr.Run(); err != nil {
		log.Fatalf("Search service failed to run: %v", err)
	}
}
