package db

import (
	"database/sql"
	"fmt"
	"os"

	"github.com/go-sql-driver/mysql"
)

func Open() (*sql.DB, error) {
	cfg := mysql.NewConfig()
	cfg.User      = os.Getenv("DB_USER")
	cfg.Passwd    = os.Getenv("DB_PASS")
	cfg.Net       = "tcp"
	cfg.Addr      = os.Getenv("DB_HOST")
	cfg.DBName    = os.Getenv("DB_NAME")
	cfg.ParseTime = true

	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, fmt.Errorf("db open: %w", err)
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	return db, nil
}
