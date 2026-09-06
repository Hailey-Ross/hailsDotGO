package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	// Event reminders turn an IANA zone id sent by the app into an instant with
	// time.LoadLocation, which otherwise reads the host's zoneinfo database. The
	// VPS has one; a slim container image would not, and every subscribe would
	// 400 with nothing to explain it. Roughly 450 KB of binary to take that whole
	// failure mode off the table.
	_ "time/tzdata"

	"pogo.hails.cc/internal/auth"
	"pogo.hails.cc/internal/costumes"
	appdb "pogo.hails.cc/internal/db"
	"pogo.hails.cc/internal/i18n"
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

	warnPendingMigrations(db)

	// Register runtime locales (created through the translator workflow)
	// before loading overlays. A missing locales table is tolerated so the
	// app still boots before the migration is applied.
	if rows, err := db.Query(`SELECT code FROM locales`); err != nil {
		log.Printf("i18n: load locales table: %v (continuing with embedded locales only)", err)
	} else {
		for rows.Next() {
			var code string
			if rows.Scan(&code) != nil {
				continue
			}
			if i18n.IsSupported(code) {
				continue
			}
			if err := i18n.RegisterLocale(code); err != nil {
				log.Printf("i18n: register locale %q: %v", code, err)
			}
		}
		rows.Close()
	}

	// Approved translation overrides live outside the binary so they survive
	// redeploys; defaults to ./locales under the working directory.
	i18n.Init(os.Getenv("LOCALES_DIR"))

	// Costume labels named in the admin panel, same idea: labels.json is compiled in, so runtime
	// names live in an overlay merged over it. Defaults to ./costumes.
	costumes.Init(os.Getenv("COSTUMES_DIR"))

	csrfKey, err := loadCSRFKey()
	if err != nil {
		log.Fatalf("csrf key: %v", err)
	}

	store := pogodata.New()

	// The router is built BEFORE the store is started, not after. server.New
	// registers the store's events-applied hook, and the startup events fetch runs
	// on its own goroutine with retries: starting the store first is a race in
	// which the first feed of the process can land with nothing listening, and the
	// event reminders it should have re-pinned wait for the 30 minute tick.
	handler := server.New(store, db, csrfKey)
	store.Start()

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           handler,
		ReadHeaderTimeout: 15 * time.Second,
		// Without a ReadTimeout, a client that sends headers promptly and then dribbles
		// its body one byte at a time holds the connection open indefinitely. Generous
		// at 2 minutes so an 8 MB screenshot upload over a poor mobile connection still
		// completes; the body size caps in server.go bound the volume, this bounds time.
		ReadTimeout:    2 * time.Minute,
		MaxHeaderBytes: 1 << 20,
		WriteTimeout:   90 * time.Second,
		IdleTimeout:    60 * time.Second,
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
		log.Println("WARNING: CSRF_KEY not set, generating a random key. CSRF tokens will not survive restarts.")
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

// warnPendingMigrations checks the columns this build's queries name and shouts
// if they are absent.
//
// Nothing runs migrations for us: deploy.ps1 uploads a binary and restarts the
// service, and cmd/migrate is a separate thing a person has to remember. So
// binary before migration is the DEFAULT ordering, not the unlucky one, and the
// failure is not confined to a new feature: a query naming a missing column
// takes the IV calculator's save button down with it, and the browser renders
// that as "log in", which is the least diagnosable message available.
//
// It logs rather than refusing to start. A site that boots with one broken
// feature and a loud journal line is better than one that will not boot at all,
// and the operator sees this within seconds of the restart.
func warnPendingMigrations(db *sql.DB) {
	// feature names what actually breaks, because the operator reading the journal is
	// trying to match a bug report to a cause. A message that always blamed the same two
	// features would point at the wrong one the moment a third entry was added.
	required := []struct{ table, column, feature string }{
		{"user_pokemon_box", "is_shadow", "the Pokemon box and saving from the IV calculator"},
		{"user_pokemon_box", "is_purified", "the Pokemon box and saving from the IV calculator"},
		{"user_request_tokens", "token", "adding a shiny from the app"},
	}
	for _, r := range required {
		var n int
		err := db.QueryRow(`
			SELECT COUNT(*) FROM information_schema.COLUMNS
			 WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND COLUMN_NAME = ?`,
			r.table, r.column,
		).Scan(&n)
		if err != nil {
			log.Printf("migration check: could not inspect %s.%s: %v", r.table, r.column, err)
			continue
		}
		if n == 0 {
			log.Printf("MIGRATION PENDING: %s.%s is missing. This build's queries need it, "+
				"so %s will fail until you run `go run ./cmd/migrate`.",
				r.table, r.column, r.feature)
		}
	}
}
