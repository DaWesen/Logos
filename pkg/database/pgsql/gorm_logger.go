package pgsql

import (
	"context"
	"fmt"
	"strings"
	"time"

	"Logos/pkg/logger"

	gormlogger "gorm.io/gorm/logger"
)

type gormLogAdapter struct {
	SlowThreshold time.Duration
}

func newGormLogAdapter() gormlogger.Interface {
	return &gormLogAdapter{SlowThreshold: 200 * time.Millisecond}
}

func (l *gormLogAdapter) LogMode(level gormlogger.LogLevel) gormlogger.Interface {
	return l
}

func (l *gormLogAdapter) Info(ctx context.Context, msg string, data ...interface{}) {
	logger.Info(fmt.Sprintf(msg, data...))
}

func (l *gormLogAdapter) Warn(ctx context.Context, msg string, data ...interface{}) {
	logger.Warn(fmt.Sprintf(msg, data...))
}

func (l *gormLogAdapter) Error(ctx context.Context, msg string, data ...interface{}) {
	logger.Error(fmt.Sprintf(msg, data...))
}

func (l *gormLogAdapter) Trace(ctx context.Context, begin time.Time, fc func() (sql string, rowsAffected int64), err error) {
	elapsed := time.Since(begin)
	sql, rows := fc()

	sqlStr := strings.ReplaceAll(strings.ReplaceAll(sql, "\n", " "), "\t", " ")
	sqlStr = strings.TrimSpace(sqlStr)

	if err != nil {
		logger.Error("SQL执行失败",
			logger.StringField("sql", sqlStr),
			logger.StringField("latency", elapsed.String()),
			logger.Int64Field("rows", rows),
			logger.ErrorField(err),
		)
		return
	}

	if elapsed > l.SlowThreshold {
		logger.Warn("慢SQL",
			logger.StringField("sql", sqlStr),
			logger.StringField("latency", elapsed.String()),
			logger.Int64Field("rows", rows),
		)
	}
}