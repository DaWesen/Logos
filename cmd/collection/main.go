package main

import (
	"Noah/config"
	"log"

	"github.com/cloudwego/kitex/pkg/rpcinfo"
	"github.com/cloudwego/kitex/server"
	etcd "github.com/kitex-contrib/registry-etcd"
)

func main() {
	cfg := config.GetConfig()

	r, err := etcd.NewEtcdRegistry(cfg.Etcd.Endpoints)
	if err != nil {
		log.Fatalf("Failed to create etcd registry: %v", err)
	}

	svr := server.NewServer(
		server.WithRegistry(r),
		server.WithServerBasicInfo(&rpcinfo.EndpointBasicInfo{ServiceName: "collection"}),
	)

	if err := svr.Run(); err != nil {
		log.Fatalf("Collection service failed to run: %v", err)
	}
}
