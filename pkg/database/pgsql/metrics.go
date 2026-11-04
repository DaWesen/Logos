package pgsql

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"Logos/pkg/obs"

	"gorm.io/gorm"
)

// slowQueryThreshold 与 GORM 日志里的慢 SQL 阈值保持一致。
// 日志用于排查单条 SQL，指标用于回答「慢查询是不是变多了」。
const slowQueryThreshold = 200 * time.Millisecond

const dbMetricsStartKey = "logos:db:metrics:start"

// registerDBMetrics 给 GORM 的所有写/读操作挂上耗时与慢查询打点。
//
// 放在数据库初始化层而不是 DAO 层：新增服务、新增 DAO、临时写 Raw SQL
// 都不需要记得埋点，避免「有的查询有指标、有的没有」。
func registerDBMetrics(db *gorm.DB) {
	if db == nil {
		return
	}

	// GORM 的 processor 类型未导出，无法写成通用 helper 参数；
	// 这里显式给每个 processor 挂 before/after，避免依赖内部类型。
	_ = db.Callback().Query().Before("gorm:query").Register("logos:db:metrics:before:query", dbBeforeCallback)
	_ = db.Callback().Query().After("gorm:query").Register("logos:db:metrics:after:query", dbAfterCallback("query"))

	_ = db.Callback().Create().Before("gorm:create").Register("logos:db:metrics:before:create", dbBeforeCallback)
	_ = db.Callback().Create().After("gorm:create").Register("logos:db:metrics:after:create", dbAfterCallback("create"))

	_ = db.Callback().Update().Before("gorm:update").Register("logos:db:metrics:before:update", dbBeforeCallback)
	_ = db.Callback().Update().After("gorm:update").Register("logos:db:metrics:after:update", dbAfterCallback("update"))

	_ = db.Callback().Delete().Before("gorm:delete").Register("logos:db:metrics:before:delete", dbBeforeCallback)
	_ = db.Callback().Delete().After("gorm:delete").Register("logos:db:metrics:after:delete", dbAfterCallback("delete"))

	_ = db.Callback().Row().Before("gorm:row").Register("logos:db:metrics:before:row", dbBeforeCallback)
	_ = db.Callback().Row().After("gorm:row").Register("logos:db:metrics:after:row", dbAfterCallback("row"))
}

// registerDBPoolStats 把 database/sql 的连接池快照接到 OTel。
// 采集发生在 scrape 时，不需要额外 goroutine。
func registerDBPoolStats(sqlDB *sql.DB) {
	if sqlDB == nil {
		return
	}

	obs.RegisterDBPoolStats(func() obs.DBPoolStats {
		stats := sqlDB.Stats()
		return obs.DBPoolStats{
			OpenConnections:     int64(stats.OpenConnections),
			InUse:               int64(stats.InUse),
			Idle:                int64(stats.Idle),
			WaitCount:           stats.WaitCount,
			WaitDurationSeconds: stats.WaitDuration.Seconds(),
		}
	})
}

func dbBeforeCallback(tx *gorm.DB) {
	if tx.Statement == nil {
		return
	}
	tx.Statement.Settings.Store(dbMetricsStartKey, time.Now())
}

func dbAfterCallback(operation string) func(*gorm.DB) {
	return func(tx *gorm.DB) {
		if tx.Statement == nil {
			return
		}
		value, ok := tx.Statement.Settings.Load(dbMetricsStartKey)
		if !ok {
			return
		}
		tx.Statement.Settings.Delete(dbMetricsStartKey)

		start, ok := value.(time.Time)
		if !ok {
			return
		}
		elapsed := time.Since(start)

		ctx := tx.Statement.Context
		if ctx == nil {
			ctx = context.Background()
		}

		table := tx.Statement.Table
		if table == "" {
			table = "unknown"
		}

		obs.RecordDBQuery(ctx, operation, table, dbResult(tx.Error), elapsed.Seconds())
		if elapsed >= slowQueryThreshold {
			obs.RecordDBSlowQuery(ctx, operation, table)
		}
	}
}

// dbResult 把 GORM 错误归类成语义标签。
// RecordNotFound 是业务上的「没有」，不是数据库故障；
// 算进 failed 会让空查询/缓存未命中污染真实错误率。
func dbResult(err error) string {
	if err == nil || errors.Is(err, gorm.ErrRecordNotFound) {
		return "ok"
	}
	return "failed"
}
