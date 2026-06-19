package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"Logos/config"
	"Logos/internal/service/messaging/qqbridge"
	"Logos/pkg/cache"
	"Logos/pkg/client"
	"Logos/pkg/logger"

	zero "github.com/wdvxdr1123/ZeroBot"
	"github.com/wdvxdr1123/ZeroBot/driver"
)

func main() {
	cfg := config.GetConfig()

	logger.InitLogger()

	if !cfg.QQBridge.Enabled {
		log.Fatal("QQ Bridge 未启用，请在配置中设置 qqbridge.enabled = true")
	}

	logger.Info("启动 QQ Bridge 服务",
		logger.StringField("ws_forward_url", cfg.QQBridge.WSForwardURL),
		logger.IntField("ws_port", cfg.QQBridge.WSPort))

	// 预热 QQ-Bot 绑定缓存
	warmupBotBindings()

	bridge := qqbridge.NewBridge(cfg)

	// 初始化 Bot gRPC 客户端（用于快速路径直接调用 Bot）
	botClient, botErr := client.NewBotClientFromConfig(cfg)
	if botErr != nil {
		logger.Warn("连接Bot服务失败，QQ快速路径不可用，Bot回复将走Chat Service慢路径",
			logger.ErrorField(botErr))
	} else {
		bridge.SetBotClient(botClient)
		logger.Info("Bot服务客户端已连接，QQ快速路径已启用")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 启动 Kafka 订阅（Logos → QQ 方向）
	if err := bridge.Start(ctx); err != nil {
		log.Fatalf("启动 QQ Bridge Kafka 订阅失败: %v", err)
	}

	// 注册 ZeroBot 插件（QQ → Logos 方向）
	plugin := bridge.GetPlugin()
	plugin.Register()

	logger.Info("QQ Bridge 服务已启动，正在连接 LLOneBot...")

	// 启动 ZeroBot 引擎
	// 支持两种模式：
	// 1. WS Client: 主动连接 LLOneBot（适用于 LLOneBot 作为 WS Server）
	// 2. WS Server: 等待 LLOneBot 连接（适用于 LLOneBot 配置反向 WS）
	go func() {
		if cfg.QQBridge.WSPort > 0 {
			// WS Server 模式：LLOneBot 主动连接过来
			// NewWebSocketServer 的 url 参数是监听地址，格式为 "host:port"
			wsAddr := fmt.Sprintf("0.0.0.0:%d", cfg.QQBridge.WSPort)
			logger.Info("使用 WebSocket Server 模式",
				logger.StringField("addr", wsAddr))

			zero.RunAndBlock(&zero.Config{
				NickName:   []string{"LogosBot"},
				SuperUsers: []int64{},
				Driver:     []zero.Driver{driver.NewWebSocketServer(cfg.QQBridge.WSPort, wsAddr, "")},
			}, nil)
		} else {
			// WS Client 模式：主动连接 LLOneBot
			wsURL := cfg.QQBridge.WSForwardURL
			if wsURL == "" {
				wsURL = "ws://127.0.0.1:3001"
			}
			logger.Info("使用 WebSocket Client 模式",
				logger.StringField("url", wsURL))

			zero.RunAndBlock(&zero.Config{
				NickName:   []string{"LogosBot"},
				SuperUsers: []int64{},
				Driver:     []zero.Driver{driver.NewWebSocketClient(wsURL, "")},
			}, nil)
		}
	}()

	logger.Info("QQ Bridge 服务已完全启动")

	// 等待退出信号
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("正在关闭 QQ Bridge 服务...")
	bridge.Stop()
	cancel()

	logger.Info("QQ Bridge 服务已关闭")
}

// warmupBotBindings 从数据库加载 Bot 的 QQ 绑定关系到 Redis 缓存
// 这样 findBotByQQNumber 就能通过缓存快速查找
func warmupBotBindings() {
	redis := cache.NewRedisCache()
	ctx := context.Background()

	// TODO: 当 Bot Service 提供了 gRPC 接口后，这里应该调用 ListBots
	// 遍历所有绑定了 QQ 号的 Bot，写入 Redis 缓存
	// 当前先跳过，依赖 Bot 创建/更新时写入缓存

	_ = redis
	_ = ctx
}
