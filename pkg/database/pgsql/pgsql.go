package pgsql

import (
	"sync"
	"time"

	"Noah/config"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

var (
	db     *gorm.DB
	dbOnce sync.Once
)

func InitPostgres() (*gorm.DB, error) {
	var err error
	dbOnce.Do(func() {
		cfg := config.GetConfig()
		postgresConfig := cfg.Database.Postgres

		dsn := cfg.GetPostgresDSN()

		db, openErr := gorm.Open(postgres.Open(dsn), &gorm.Config{})
		if openErr != nil {
			err = openErr
			return
		}

		// 配置连接池
		sqlDB, dbErr := db.DB()
		if dbErr != nil {
			err = dbErr
			return
		}

		sqlDB.SetMaxOpenConns(postgresConfig.MaxOpenConns)
		sqlDB.SetMaxIdleConns(postgresConfig.MaxIdleConns)
		sqlDB.SetConnMaxLifetime(time.Duration(postgresConfig.ConnMaxLifetime) * time.Second)
	})

	return db, err
}
