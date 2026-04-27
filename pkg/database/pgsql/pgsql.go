package pgsql

import (
	"fmt"
	"time"

	"Logos/config"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func InitPostgres() (*gorm.DB, error) {
	cfg := config.GetConfig()
	postgresConfig := cfg.Database.Postgres

	dsn := cfg.GetPostgresDSN()

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("failed to open postgres: %w", err)
	}

	// 配置连接池
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get underlying sql.DB: %w", err)
	}

	sqlDB.SetMaxOpenConns(postgresConfig.MaxOpenConns)
	sqlDB.SetMaxIdleConns(postgresConfig.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(time.Duration(postgresConfig.ConnMaxLifetime) * time.Second)

	// 验证数据库连接
	if err := sqlDB.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping postgres: %w", err)
	}

	return db, nil
}
