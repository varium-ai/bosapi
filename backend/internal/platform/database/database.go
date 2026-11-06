package database

import (
	"fmt"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// Open creates a MySQL Gorm connection with bounded, explicit pool settings.
func Open(dsn string) (*gorm.DB, error) {
	if dsn == "" {
		return nil, fmt.Errorf("bos_database_dsn is required")
	}
	database, err := gorm.Open(mysql.Open(dsn), &gorm.Config{TranslateError: true})
	if err != nil {
		return nil, fmt.Errorf("open mysql database: %w", err)
	}
	sqlDatabase, err := database.DB()
	if err != nil {
		return nil, fmt.Errorf("get sql database: %w", err)
	}
	sqlDatabase.SetMaxOpenConns(25)
	sqlDatabase.SetMaxIdleConns(10)
	sqlDatabase.SetConnMaxLifetime(30 * time.Minute)
	sqlDatabase.SetConnMaxIdleTime(5 * time.Minute)
	return database, nil
}
