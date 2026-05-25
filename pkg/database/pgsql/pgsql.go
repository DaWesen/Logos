package pgsql

import (
	"fmt"
	"time"

	"Logos/config"
	"Logos/pkg/logger"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func InitPostgres() (*gorm.DB, error) {
	cfg := config.GetConfig()
	postgresConfig := cfg.Database.Postgres

	dsn := cfg.GetPostgresDSN()

	var db *gorm.DB
	var err error

	for i := 0; i < 30; i++ {
		db, err = gorm.Open(postgres.Open(dsn), &gorm.Config{
			Logger: newGormLogAdapter(),
		})
		if err == nil {
			sqlDB, pingErr := db.DB()
			if pingErr == nil {
				pingErr = sqlDB.Ping()
			}
			if pingErr == nil {
				sqlDB.SetMaxOpenConns(postgresConfig.MaxOpenConns)
				sqlDB.SetMaxIdleConns(postgresConfig.MaxIdleConns)
				sqlDB.SetConnMaxLifetime(time.Duration(postgresConfig.ConnMaxLifetime) * time.Second)
				logger.Info("PostgreSQL连接成功")
				return db, nil
			}
			logger.Warn("PostgreSQL Ping失败，重试中...", logger.IntField("attempt", i+1), logger.ErrorField(pingErr))
		} else {
			logger.Warn("PostgreSQL连接失败，重试中...", logger.IntField("attempt", i+1), logger.ErrorField(err))
		}
		time.Sleep(2 * time.Second)
	}

	return nil, fmt.Errorf("failed to connect postgres after 30 attempts: %w", err)
}
