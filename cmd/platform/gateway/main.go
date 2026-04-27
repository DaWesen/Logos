package main

import (
	"Logos/config"
	"Logos/internal/service/platform/gateway/router"
	"Logos/pkg/logger"
	"log"
	"net/http"
)

func main() {
	cfg := config.GetConfig()

	logger.InitLogger()

	r := router.SetupRouter()

	log.Printf("Gateway server starting on :%d", cfg.Ports.Gateway)
	if err := http.ListenAndServe(cfg.GetGatewayAddr(), r); err != nil {
		log.Fatalf("Gateway server failed to run: %v", err)
	}
}
