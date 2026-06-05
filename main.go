package main

import (
	"context"
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

	store := pogodata.New()
	store.Start()

	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      server.New(store, db),
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
