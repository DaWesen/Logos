package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"Logos/config"
	"Logos/internal/service/messaging/qqbridge"
	"Logos/pkg/cache"
	"Logos/pkg/client"
	"Logos/pkg/database/pgsql"
	"Logos/pkg/logger"

	zero "github.com/wdvxdr1123/ZeroBot"
	"github.com/wdvxdr1123/ZeroBot/driver"
	"gorm.io/gorm"
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

	// 预热 QQ-Bot 绑定缓存；数据库不可用时返回 nil，Bridge 仍可启动。
	db := warmupBotBindings()

	bridge := qqbridge.NewBridge(cfg)
	if db != nil {
		bridge.SetBindingLookup(qqbridge.BotBindingLookup{DB: db})
	}

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
func warmupBotBindings() *gorm.DB {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 预热是「尽力而为」：数据库不可用不应该阻塞 Bridge 启动。
	// 这里用短超时直接连一次，失败就跳过；运行时消息仍可走缓存或退化为普通用户。
	db, err := pgsql.InitPostgresWithRetry(ctx, 1)
	if err != nil {
		logger.Warn("QQ-Bot绑定预热跳过：PostgreSQL不可用", logger.ErrorField(err))
		return nil
	}

	bindings, err := qqbridge.LoadBotBindings(ctx, db)
	if err != nil {
		logger.Warn("QQ-Bot绑定预热跳过：读取失败", logger.ErrorField(err))
		return db
	}

	written, err := qqbridge.WarmupBotBindings(ctx, bindings, cache.NewRedisCache())
	if err != nil {
		logger.Warn("QQ-Bot绑定预热部分失败",
			logger.IntField("written", written),
			logger.ErrorField(err))
		return db
	}
	logger.Info("QQ-Bot绑定预热完成", logger.IntField("count", written))
	return db
}
