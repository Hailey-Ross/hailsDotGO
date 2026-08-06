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

	// DB_HOST is a different machine from the app, so this connection crosses a
	// network we do not control, and GetSession runs on nearly every request: without
	// this, every live session token and every bcrypt hash travelled in cleartext.
	//
	// "preferred" encrypts whenever the server offers TLS and falls back to plaintext
	// when it does not, so it cannot take the site down on deploy. It deliberately
	// does NOT authenticate the server, so it stops passive sniffing but not an active
	// machine-in-the-middle. DB_TLS overrides it: set DB_TLS=true once the server has
	// a certificate valid for the DB_HOST name to get verification as well.
	cfg.TLSConfig = "preferred"
	if mode := os.Getenv("DB_TLS"); mode != "" {
		cfg.TLSConfig = mode
	}

	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, fmt.Errorf("db open: %w", err)
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	return db, nil
}
