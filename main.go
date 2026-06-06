package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"pogo.hails.cc/internal/auth"
	appdb "pogo.hails.cc/internal/db"
	"pogo.hails.cc/internal/pogodata"
	"pogo.hails.cc/internal/server"
)

func main() {
	auth.SuperadminUser = os.Getenv("SUPERADMIN_USER")
	if auth.SuperadminUser == "" {
		log.Fatal("SUPERADMIN_USER env var is not set")
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	db, err := appdb.Open()
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	if err := db.Ping(); err != nil {
		log.Fatalf("db ping: %v", err)
	}
	defer db.Close()

	csrfKey, err := loadCSRFKey()
	if err != nil {
		log.Fatalf("csrf key: %v", err)
	}

	store := pogodata.New()
	store.Start()

	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      server.New(store, db, csrfKey),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.Printf("Listening on :%s", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("shutdown: %v", err)
	}
}

func loadCSRFKey() ([]byte, error) {
	keyHex := os.Getenv("CSRF_KEY")
	if keyHex == "" {
		log.Println("WARNING: CSRF_KEY not set — generating random key. CSRF tokens will not survive restarts.")
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		return key, nil
	}
	key, err := hex.DecodeString(keyHex)
	if err != nil || len(key) != 32 {
		log.Fatal("CSRF_KEY must be a 64-character hex string (32 bytes). Generate with: openssl rand -hex 32")
	}
	return key, nil
}
