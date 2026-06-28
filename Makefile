.PHONY: dev build run clean migrate migrate-status migrate-build

# Install JS deps (run once after cloning)
setup:
	npm install

# Build TypeScript + Go binary
build:
	npm run build
	go build -o hailsDotGO .

# Dev mode: watch TS + run Go server (requires two terminals, or use tmux/concurrently)
dev:
	npm run watch &
	go run .

# Run a previously built binary
run:
	./hailsDotGO

clean:
	rm -f hailsDotGO static/js/*.js migrate

# Upgrade an existing database to the latest schema (reads env: DB_HOST/DB_USER/DB_PASS/DB_NAME).
# Pass extra flags via ARGS, e.g.: make migrate ARGS="-from v0.1.3a"
migrate:
	go run ./cmd/migrate $(ARGS)

# Show applied vs pending migrations.
migrate-status:
	go run ./cmd/migrate -status

# Build a standalone migrate binary (handy for running on the server).
migrate-build:
	go build -o migrate ./cmd/migrate
