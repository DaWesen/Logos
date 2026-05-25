package main

import (
	"context"
	"log"

	"Logos/config"
	"Logos/internal/mcp"
	"Logos/internal/service/ai/mcp/dao"
	"Logos/internal/service/ai/mcp/handler"
	"Logos/internal/service/ai/mcp/model"
	"Logos/internal/service/ai/mcp/service"
	"Logos/pkg/database/pgsql"
	"Logos/pkg/eino"
	"Logos/pkg/grpcserver"
	"Logos/pkg/logger"
	"Logos/pkg/obs"
	pb "Logos/proto_gen/mcp"

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

	var einoClient *eino.EinoManager
	einoClient, err = eino.InitEino()
	if err != nil {
		log.Printf("Failed to init eino: %v", err)
	}

	repo := dao.NewMCPRepository(db)

	mcpService := service.NewMCPService(repo, einoClient)

	svcRepo := dao.NewMCPServiceRepository(db)
	clientMgr := mcp.NewMCPClientManager()
	mcpServiceSvc := service.NewMCPServiceService(svcRepo, clientMgr)

	shutdown, serverOpt, _ := obs.InitGRPCProvider("mcp")
	defer shutdown(context.Background())

	if err := grpcserver.StartServer(grpcserver.ServerConfig{
		ServiceName: "logos.mcp",
		Port:        cfg.Ports.MCP,
		Etcd:        grpcserver.EtcdConfig{Endpoints: cfg.Etcd.Endpoints},
	}, func(s *grpc.Server) {
		pb.RegisterMCPServiceServer(s, &handler.MCPServiceImpl{
			MCPService:    mcpService,
			MCPServiceSvc: mcpServiceSvc,
		})
	}, serverOpt); err != nil {
		log.Fatalf("MCP service failed to run: %v", err)
	}
}
