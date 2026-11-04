// Package health 的常用依赖探针。
//
// 这里只提供「最小、带超时、可组合」的检查函数，不依赖具体服务代码：
// 各服务在自己的 main 里把已经初始化好的 db/cache 传进来，避免健康检查
// 又去新建一条连接池（那会让探针成为额外的资源消耗者）。
package health

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"Logos/pkg/cache"

	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/health/grpc_health_v1"
	"gorm.io/gorm"
)

// Postgres 返回一个基于 *sql.DB 的 PostgreSQL 探针。
//
// 用 PingContext 而不是复用业务查询：探针要回答的是「连接池能不能拿到可用连接」，
// 不应该因为某张表/某条索引的迁移状态而误判整个服务不可用。
func Postgres(db *sql.DB) CheckFunc {
	return func(ctx context.Context) error {
		if db == nil {
			return fmt.Errorf("postgres: nil db")
		}
		// PingContext 会从池里取连接；池被业务请求占满时它会等待，
		// 因此必须由外层探测预算兜住，不能在这里再套一层固定超时。
		if err := db.PingContext(ctx); err != nil {
			return fmt.Errorf("postgres: %w", err)
		}
		return nil
	}
}

// GormPostgres 返回一个基于 *gorm.DB 的探针。
//
// 各服务拿到的都是 *gorm.DB，但健康检查只关心底层连接池；
// 在这里做一次转换，避免每个 main 都写一遍 db.DB() 的错误处理。
func GormPostgres(db *gorm.DB) CheckFunc {
	return func(ctx context.Context) error {
		if db == nil {
			return fmt.Errorf("postgres: nil gorm db")
		}
		sqlDB, err := db.DB()
		if err != nil {
			return fmt.Errorf("postgres: %w", err)
		}
		return Postgres(sqlDB)(ctx)
	}
}

// Redis 返回一个基于 cache.Cache 的 Redis 探针。
//
// Ping 自带 200ms 独立预算（与缓存读一致），不会因为 Redis 半死把探针拖到
// 外层总超时；超时即视为未就绪，由 Registry 汇总。
func Redis(c cache.Cache) CheckFunc {
	return func(ctx context.Context) error {
		if c == nil {
			return fmt.Errorf("redis: cache unavailable")
		}
		pingCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		defer cancel()
		if err := c.Ping(pingCtx); err != nil {
			return fmt.Errorf("redis: %w", err)
		}
		return nil
	}
}

// Func 把无 ctx 的快速检查适配成 CheckFunc。
// 仅用于本身不会阻塞的检查（例如「配置是否已加载」），
// 任何可能做 IO 的检查都应自己接收 ctx。
func Func(fn func() error) CheckFunc {
	return func(context.Context) error {
		if fn == nil {
			return nil
		}
		return fn()
	}
}

// Timeout 给单个检查加独立上限，避免一个慢依赖吃掉整个探测预算。
// 外层 Registry 仍有总预算；这里的 timeout 只用于「这一项最多等多久」。
func Timeout(d time.Duration, fn CheckFunc) CheckFunc {
	return func(ctx context.Context) error {
		if fn == nil {
			return nil
		}
		if d <= 0 {
			return fn(ctx)
		}
		checkCtx, cancel := context.WithTimeout(ctx, d)
		defer cancel()
		return fn(checkCtx)
	}
}

// GRPC 返回一个基于 gRPC 健康协议的下游探针。
//
// 只看 ClientConn 状态是不够的：连接可以是 READY，但服务端已经把具名服务
// 标成 NOT_SERVING（依赖挂了、正在停机）。健康协议是唯一能区分这两者的标准入口。
// service 为空时查询进程级健康。
func GRPC(conn *grpc.ClientConn, service string) CheckFunc {
	return func(ctx context.Context) error {
		if conn == nil {
			return fmt.Errorf("grpc: nil connection")
		}
		// 连接尚未建立时直接判失败，不要让探针触发一次新的拨号：
		// 探针应该是观察者，不应该改变被观察系统的状态。
		state := conn.GetState()
		if state == connectivity.Shutdown {
			return fmt.Errorf("grpc: connection closed")
		}
		resp, err := grpc_health_v1.NewHealthClient(conn).Check(ctx, &grpc_health_v1.HealthCheckRequest{
			Service: service,
		})
		if err != nil {
			return fmt.Errorf("grpc: health check: %w", err)
		}
		if resp.GetStatus() != grpc_health_v1.HealthCheckResponse_SERVING {
			return fmt.Errorf("grpc: status %s", resp.GetStatus().String())
		}
		return nil
	}
}
