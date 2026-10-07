package main

import (
	"Logos/config"
	"Logos/internal/service/messaging/types"
	"Logos/internal/service/platform/gateway"
	"Logos/internal/service/platform/gateway/middleware"
	"Logos/internal/service/platform/gateway/router"
	"Logos/internal/service/platform/gateway/tcp"
	"Logos/internal/service/platform/gateway/websocket"
	"Logos/pkg/cache"
	"Logos/pkg/client"
	"Logos/pkg/logger"
	"Logos/pkg/obs"
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

func main() {
	cfg := config.GetConfig()

	logger.InitLogger()

	shutdown, _, _ := obs.InitGRPCProvider("gateway")
	defer shutdown(context.Background())

	_ = obs.InitServiceMeters("gateway")

	// 创建 websocket handler
	wsHandler := websocket.NewHandler()

	// 初始化跨节点广播器（多节点水平扩展）
	// enable_broadcast=false 或 redis 不可用时降级为纯本地投递
	broadcasterCleanup := initBroadcaster(cfg)

	userClient, userErr := client.NewUserClientFromConfig(cfg)
	if userErr != nil {
		logger.Warn("连接User服务失败，发送者头像信息不可用", logger.ErrorField(userErr))
	} else {
		wsHandler.SetUserClient(userClient)
		logger.Info("User服务客户端已连接")
	}

	monitoringClient, monitoringErr := client.NewMonitoringClientFromConfig(cfg)
	if monitoringErr != nil {
		logger.Warn("连接Monitoring服务失败，指标上报不可用", logger.ErrorField(monitoringErr))
	} else {
		middleware.InitMetricsReporter(monitoringClient)
		logger.Info("Monitoring服务客户端已连接，指标上报已启动")
		go func() {
			time.Sleep(2 * time.Second)
			if err := monitoringClient.UpdateServiceStatus(context.Background(), "logos.gateway", "UP", nil, map[string]string{
				"port": fmt.Sprintf("%d", cfg.Ports.Gateway),
			}); err != nil {
				logger.Warn("上报Gateway状态失败", logger.ErrorField(err))
			}
		}()
	}

	r := router.SetupRouter(wsHandler)

	tcpPort := cfg.Ports.Gateway + 1
	if v := os.Getenv("TCP_PORT"); v != "" {
		fmt.Sscanf(v, "%d", &tcpPort)
	}

	tcpMgr := tcp.NewConnectionManager()
	tcpHandler := tcp.NewHandler(tcpMgr)

	tcpAddr := fmt.Sprintf("0.0.0.0:%d", tcpPort)
	tcpListener, err := net.Listen("tcp", tcpAddr)
	if err != nil {
		log.Fatalf("TCP listen failed: %v", err)
	}

	// 创建一个用于退出的context和channel
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		log.Printf("TCP server starting on :%d", tcpPort)
		for {
			select {
			case <-ctx.Done():
				log.Println("TCP server stopped")
				close(done)
				return
			default:
				conn, err := tcpListener.Accept()
				if err != nil {
					select {
					case <-ctx.Done():
						return
					default:
						log.Printf("TCP accept error: %v", err)
						continue
					}
				}
				go tcpHandler.ServeTCP(conn)
			}
		}
	}()

	httpServer := &http.Server{
		Addr:    cfg.GetGatewayAddr(),
		Handler: r,
	}

	go func() {
		log.Printf("HTTP/WebSocket server starting on :%d", cfg.Ports.Gateway)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server failed: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down gateway...")
	cancel()

	middleware.StopMetricsReporter()
	if monitoringClient != nil {
		shutdownCtx, shutdownCancel2 := context.WithTimeout(context.Background(), 2*time.Second)
		_ = monitoringClient.UpdateServiceStatus(shutdownCtx, "logos.gateway", "DOWN", nil, nil)
		shutdownCancel2()
		_ = monitoringClient.Close()
	}

	// 创建一个带超时的context用于关闭操作
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	var wg sync.WaitGroup

	// 并行关闭资源，加快关闭速度
	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Println("Closing EventBus...")
		_ = types.GetEventBus().Close()
		log.Println("EventBus closed")
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Println("Closing TCP handler...")
		tcpHandler.Close()
		log.Println("TCP handler closed")
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Println("Closing WebSocket handler...")
		wsHandler.Close()
		log.Println("WebSocket handler closed")
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Println("Closing TCP listener...")
		_ = tcpListener.Close()
		log.Println("TCP listener closed")
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Println("Shutting down HTTP server...")
		_ = httpServer.Shutdown(shutdownCtx)
		log.Println("HTTP server shut down")
	}()

	// 关闭跨节点广播器（停止 redis 订阅 goroutine）
	if broadcasterCleanup != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			log.Println("Closing broadcaster...")
			broadcasterCleanup()
			log.Println("Broadcaster closed")
		}()
	}

	// 等待所有资源关闭完成，或者超时
	waitChan := make(chan struct{})
	go func() {
		wg.Wait()
		close(waitChan)
	}()

	select {
	case <-waitChan:
		log.Println("All resources closed gracefully")
	case <-shutdownCtx.Done():
		log.Println("Shutdown timed out, forcing exit")
	}

	// 等待TCP服务器退出
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
	}

	log.Println("Gateway stopped")
}

// initBroadcaster 初始化跨节点广播器并注入到 UnifiedConnectionManager
//
// 启用条件：cfg.Gateway.EnableBroadcast=true（默认 true）
// 降级策略：redis 不可用时仅警告不退出，SetBroadcaster(nil) 保持纯本地投递
//
// 返回 cleanup 函数：在 main shutdown 流程中调用以停止 redis 订阅 goroutine
// 返回 nil 表示未启动广播器（cleanup 无需调用）
func initBroadcaster(cfg *config.Config) func() {
	if !cfg.Gateway.EnableBroadcast {
		logger.Info("broadcaster disabled by config, running in local-only mode")
		gateway.GetUnifiedConnectionManager().SetBroadcaster(nil)
		return nil
	}

	// 复用 cache 单例的底层 redis client（共享连接池）
	cacheInstance := cache.NewRedisCache()
	redisCache, ok := cacheInstance.(*cache.RedisCache)
	if !ok {
		logger.Warn("broadcaster: cache instance is not *RedisCache, broadcasting disabled")
		gateway.GetUnifiedConnectionManager().SetBroadcaster(nil)
		return nil
	}
	rdb := redisCache.RawClient()

	nodeID := cfg.GetGatewayNodeID()
	channel := cfg.GetGatewayPubSubChannel()

	b := gateway.NewBroadcaster(rdb, nodeID, channel)
	gateway.GetUnifiedConnectionManager().SetBroadcaster(b)

	// 启动订阅 goroutine
	subscribeCtx, subscribeCancel := context.WithCancel(context.Background())
	go func() {
		logger.Info("broadcaster: subscribe goroutine started",
			logger.StringField("node_id", nodeID),
			logger.StringField("channel", channel))
		if err := b.Subscribe(subscribeCtx, gateway.GetUnifiedConnectionManager().HandleRemoteMessage); err != nil {
			logger.Warn("broadcaster: subscribe exited with error",
				logger.ErrorField(err))
		}
		logger.Info("broadcaster: subscribe goroutine exited")
	}()

	logger.Info("broadcaster initialized for multi-node scaling",
		logger.StringField("node_id", nodeID),
		logger.StringField("channel", channel))

	// cleanup：cancel 订阅 ctx + 关闭 pubsub
	return func() {
		subscribeCancel()
		_ = b.Close()
	}
}
