package pgsql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"Logos/pkg/obs"

	"gorm.io/gorm"
)

// slowQueryThreshold 与 GORM 日志里的慢 SQL 阈值保持一致。
// 日志用于排查单条 SQL，指标用于回答「慢查询是不是变多了」。
const slowQueryThreshold = 200 * time.Millisecond

const dbMetricsStartKey = "logos:db:metrics:start"

// callbackRegistrar 抹掉 GORM 未导出的回调类型，只保留注册能力。
//
// 每次注册都必须重新调用一次 Before/After 拿新对象：GORM 的 Register 会
// 原地改写回调对象自身的 name/handler，复用同一个对象时后一次注册会把
// 前一次登记的名字覆盖掉，链上留下两个同名回调，先注册的那个 handler 也丢了。
type callbackRegistrar interface {
	Register(name string, fn func(*gorm.DB)) error
}

// registerDBMetrics 给 GORM 的所有写/读操作挂上耗时与慢查询打点。
//
// 放在数据库初始化层而不是 DAO 层：新增服务、新增 DAO、临时写 Raw SQL
// 都不需要记得埋点，避免「有的查询有指标、有的没有」。
//
// 返回 error 而不是把 Register 的返回值丢掉：注册失败意味着某个操作从此
// 没有任何 SQL 指标，慢查询和错误率会永久缺一块，而且日志里不留痕迹。
// 调用方应告警，但不必因此阻断启动（数据库本身仍然可用）。
func registerDBMetrics(db *gorm.DB) error {
	if db == nil {
		return errors.New("register db metrics: nil gorm db")
	}

	query, create := db.Callback().Query(), db.Callback().Create()
	update, del := db.Callback().Update(), db.Callback().Delete()
	row, raw := db.Callback().Row(), db.Callback().Raw()

	processors := []struct {
		operation string
		anchor    string
		get       func(string) func(*gorm.DB)
		// newCallback 每次调用都要返回一个全新的回调对象，不能复用。
		newCallback func() callbackRegistrar
	}{
		{"query", "gorm:query", query.Get, func() callbackRegistrar { return query.Before("gorm:query") }},
		{"create", "gorm:create", create.Get, func() callbackRegistrar { return create.Before("gorm:create") }},
		{"update", "gorm:update", update.Get, func() callbackRegistrar { return update.Before("gorm:update") }},
		{"delete", "gorm:delete", del.Get, func() callbackRegistrar { return del.Before("gorm:delete") }},
		{"row", "gorm:row", row.Get, func() callbackRegistrar { return row.Before("gorm:row") }},
		// raw 覆盖 db.Exec：保留策略的批量 DELETE、启动期的表结构修正都走这里，
		// 它们往往是系统里最慢的语句，漏掉会让慢查询面板只看见业务查询。
		{"raw", "gorm:raw", raw.Get, func() callbackRegistrar { return raw.Before("gorm:raw") }},
	}

	var errs []error
	for _, p := range processors {
		// 锚点不存在时 GORM 不会报错：Before("gorm:query") 只是排不上序，
		// 我们的回调会被追加到链尾，before 在 SQL 执行之后才跑，计时约等于 0。
		// 面板上会多出一批「永远很快」的语句，比没有指标更误导，所以直接跳过。
		if p.get(p.anchor) == nil {
			errs = append(errs, fmt.Errorf(
				"%s: anchor callback %q missing, metrics not registered", p.operation, p.anchor))
			continue
		}

		beforeName := "logos:db:metrics:before:" + p.operation
		if err := p.newCallback().Register(beforeName, dbBeforeCallback); err != nil {
			errs = append(errs, fmt.Errorf("register %s: %w", beforeName, err))
		}

		afterName := "logos:db:metrics:after:" + p.operation
		if err := p.newCallback().Register(afterName, dbAfterCallback(p.operation)); err != nil {
			errs = append(errs, fmt.Errorf("register %s: %w", afterName, err))
		}
	}
	return errors.Join(errs...)
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
