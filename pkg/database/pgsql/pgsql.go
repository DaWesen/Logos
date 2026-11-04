package pgsql

import (
	"context"
	"fmt"
	"time"

	"Logos/config"
	"Logos/pkg/logger"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func InitPostgres() (*gorm.DB, error) {
	return InitPostgresWithRetry(context.Background(), 30)
}

// InitPostgresWithRetry 按指定次数重试连接。
//
// 长驻服务需要 30 次重试等依赖就绪；但「预热缓存」这类可选任务不应该
// 因为数据库没起来就阻塞启动几十秒，调用方可以传 1 并配合自己的 ctx 超时。
func InitPostgresWithRetry(ctx context.Context, attempts int) (*gorm.DB, error) {
	if attempts <= 0 {
		attempts = 1
	}
	cfg := config.GetConfig()
	postgresConfig := cfg.Database.Postgres

	dsn := cfg.GetPostgresDSN()

	var db *gorm.DB
	var err error

	for i := 0; i < attempts; i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		db, err = gorm.Open(postgres.Open(dsn), &gorm.Config{
			Logger: newGormLogAdapter(),
		})
		if err == nil {
			sqlDB, pingErr := db.DB()
			if pingErr == nil {
				pingErr = sqlDB.Ping()
			}
			if pingErr == nil {
				pool := NormalizePoolSettings(
					postgresConfig.MaxOpenConns,
					postgresConfig.MaxIdleConns,
					postgresConfig.ConnMaxLifetime,
				)
				sqlDB.SetMaxOpenConns(pool.MaxOpen)
				sqlDB.SetMaxIdleConns(pool.MaxIdle)
				sqlDB.SetConnMaxLifetime(pool.MaxLifetime)
				sqlDB.SetConnMaxIdleTime(pool.MaxIdleTime)
				registerDBMetrics(db)
				registerDBPoolStats(sqlDB)
				logger.Info("PostgreSQL连接成功",
					logger.IntField("max_open_conns", pool.MaxOpen),
					logger.IntField("max_idle_conns", pool.MaxIdle),
					logger.StringField("conn_max_lifetime", pool.MaxLifetime.String()),
					logger.StringField("conn_max_idle_time", pool.MaxIdleTime.String()))
				return db, nil
			}
			// gorm.Open 成功但 Ping 失败时，连接池已经创建好了。
			// 不关闭就丢弃会让池内连接和后台 goroutine 一直挂到进程退出；
			// 每重试一次泄漏一个池，30 次重试就是 30 个。
			if sqlDB != nil {
				_ = sqlDB.Close()
			}
			logger.Warn("PostgreSQL Ping失败，重试中...", logger.IntField("attempt", i+1), logger.ErrorField(pingErr))
		} else {
			logger.Warn("PostgreSQL连接失败，重试中...", logger.IntField("attempt", i+1), logger.ErrorField(err))
		}
		// 最后一次失败后不再等待：再等 2 秒也不会重试，只会白拖慢调用方。
		if !shouldWaitBeforeNextAttempt(i, attempts) {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}

	return nil, fmt.Errorf("failed to connect postgres after %d attempts: %w", attempts, err)
}

// shouldWaitBeforeNextAttempt 判断第 attempt 次失败后是否还需要等待。
// attempt 从 0 开始。最后一次失败后等待没有任何意义：不会再有下一次连接，
// 只会让调用方（例如可选缓存预热）白等一个退避周期。
func shouldWaitBeforeNextAttempt(attempt, attempts int) bool {
	if attempts <= 1 {
		return false
	}
	return attempt < attempts-1
}
