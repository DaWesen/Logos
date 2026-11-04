package collector

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"Logos/pkg/logger"
	"Logos/pkg/obs"

	"gorm.io/gorm"
)

// RetentionPolicy 定义监控数据的保留时长。
//
// metrics 每 30s 写入一次、logs 随业务流量增长、alerts 只增不减，
// 没有保留策略时表会无限膨胀：查询越来越慢、索引越来越大、磁盘最终写满。
// 监控系统自己把数据库撑爆，比它要监控的故障更隐蔽。
type RetentionPolicy struct {
	Metrics time.Duration
	Logs    time.Duration
	Alerts  time.Duration
}

const (
	defaultMetricsRetention = 7 * 24 * time.Hour
	defaultLogsRetention    = 7 * 24 * time.Hour
	defaultAlertsRetention  = 30 * 24 * time.Hour
)

// DefaultRetentionPolicy 是未配置时的保守默认值：
// 指标与日志保留 7 天足够排障，已解决告警保留 30 天用于复盘。
func DefaultRetentionPolicy() RetentionPolicy {
	return RetentionPolicy{
		Metrics: defaultMetricsRetention,
		Logs:    defaultLogsRetention,
		Alerts:  defaultAlertsRetention,
	}
}

// RetentionPolicyFromEnv 从环境变量读取保留天数。
// 非法/非正数回退默认值，避免一次配置笔误触发「把全部历史数据删光」。
func RetentionPolicyFromEnv() RetentionPolicy {
	p := DefaultRetentionPolicy()
	p.Metrics = retentionDaysFromEnv("MONITORING_METRICS_RETENTION_DAYS", p.Metrics)
	p.Logs = retentionDaysFromEnv("MONITORING_LOGS_RETENTION_DAYS", p.Logs)
	p.Alerts = retentionDaysFromEnv("MONITORING_ALERTS_RETENTION_DAYS", p.Alerts)
	return p
}

func retentionDaysFromEnv(name string, fallback time.Duration) time.Duration {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}
	days, err := strconv.Atoi(raw)
	if err != nil || days <= 0 {
		log.Printf("invalid %s=%q, using default %s", name, raw, fallback)
		return fallback
	}
	// 上限 10 年：再大等同于不清理，但至少不会因为溢出变成负数。
	if days > 3650 {
		days = 3650
	}
	return time.Duration(days) * 24 * time.Hour
}

// retentionDeleteSQL 构造分批删除语句。
//
// 用 id 子查询 + LIMIT，而不是一条 DELETE ... WHERE timestamp < ?：
// 一次性删除几十万行会长时间持锁、撑爆 WAL，并阻塞采集写入。
// FOR UPDATE SKIP LOCKED 让多副本同时清理时各删各的，不互相等待。
func retentionDeleteSQL(table, timeColumn, extraWhere string) string {
	return fmt.Sprintf(
		`DELETE FROM %s WHERE id IN (`+
			`SELECT id FROM %s WHERE %s < ? %s ORDER BY %s ASC LIMIT ? FOR UPDATE SKIP LOCKED)`,
		table, table, timeColumn, extraWhere, timeColumn,
	)
}

const (
	// retentionBatchSize 单条 DELETE 的行数上限。
	retentionBatchSize = 5000
	// retentionMaxBatches 单轮每个表最多删多少批（默认 5 万行），
	// 避免积压很多时一次清理占用数据库太久。
	retentionMaxBatches = 10
	// retentionRunTimeout 单轮清理的总预算。
	retentionRunTimeout = 2 * time.Minute
	// retentionInterval 清理周期。按天级保留策略，每小时跑一次足够。
	retentionInterval = time.Hour
)

type RetentionCleaner struct {
	db               *gorm.DB
	policy           RetentionPolicy
	interval         time.Duration
	batchSize        int
	maxBatchesPerRun int
	now              func() time.Time
}

func NewRetentionCleaner(db *gorm.DB, policy RetentionPolicy) *RetentionCleaner {
	return &RetentionCleaner{
		db:               db,
		policy:           policy,
		interval:         retentionInterval,
		batchSize:        retentionBatchSize,
		maxBatchesPerRun: retentionMaxBatches,
		now:              time.Now,
	}
}

// Start 启动清理循环：启动即跑一次（回收长期积压），之后每小时一次。
func (c *RetentionCleaner) Start(ctx context.Context) {
	if c == nil || c.db == nil {
		return
	}
	log.Printf("[Retention] started: metrics=%s logs=%s alerts=%s",
		c.policy.Metrics, c.policy.Logs, c.policy.Alerts)

	c.runOnce(ctx)

	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Println("[Retention] stopped")
			return
		case <-ticker.C:
			c.runOnce(ctx)
		}
	}
}

func (c *RetentionCleaner) runOnce(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, retentionRunTimeout)
	defer cancel()

	specs := []struct {
		kind      string
		table     string
		timeCol   string
		extra     string
		retention time.Duration
	}{
		{kind: "metrics", table: "metrics", timeCol: "timestamp", retention: c.policy.Metrics},
		{kind: "logs", table: "logs", timeCol: "timestamp", retention: c.policy.Logs},
		// 只清理已解决告警：未解决告警可能仍在排查中，删掉会丢失上下文。
		{kind: "alerts", table: "alerts", timeCol: "timestamp", extra: "AND resolved = true", retention: c.policy.Alerts},
	}

	for _, spec := range specs {
		if spec.retention <= 0 {
			continue
		}
		cutoff := c.now().Add(-spec.retention).UnixMilli()
		deleted, err := c.deleteBatches(ctx, spec.table, spec.timeCol, spec.extra, cutoff)
		if err != nil {
			obs.RecordMonitoringRetention(ctx, spec.kind, "failed", deleted)
			logger.Error("Monitoring retention delete failed",
				logger.StringField("kind", spec.kind),
				logger.Int64Field("deleted", deleted),
				logger.ErrorField(err))
			continue
		}
		obs.RecordMonitoringRetention(ctx, spec.kind, "ok", deleted)
		if deleted > 0 {
			logger.Info("Monitoring retention cleanup",
				logger.StringField("kind", spec.kind),
				logger.Int64Field("deleted", deleted),
				logger.StringField("retention", spec.retention.String()))
		}
	}
}

// deleteBatches 分批删除直到没有更多数据、达到单轮上限或 ctx 取消。
// 返回已删除行数；中途出错时也返回已完成的部分，便于指标反映真实进度。
func (c *RetentionCleaner) deleteBatches(ctx context.Context, table, timeCol, extra string, cutoff int64) (int64, error) {
	query := retentionDeleteSQL(table, timeCol, extra)

	var total int64
	for batch := 0; batch < c.maxBatchesPerRun; batch++ {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		res := c.db.WithContext(ctx).Exec(query, cutoff, c.batchSize)
		if res.Error != nil {
			return total, res.Error
		}
		total += res.RowsAffected
		if res.RowsAffected < int64(c.batchSize) {
			return total, nil
		}
	}
	return total, nil
}
