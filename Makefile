.PHONY: dev build run clean migrate migrate-status migrate-build costumes costumes-check

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

# Rebuild internal/costumes/catalog.json from the mined PokeMiners asset tree: which costume
# codes exist, which species can wear them, and whether the shiny art is there. Run this after
# an event drops new costumes, then give any REVIEW entries a label in labels.json.
# Deliberately NOT a dependency of `build`: the build must never need the network.
# Writes nothing when only the asset pin moved (pinned URLs are immutable, so an older pin serves
# every sprite correctly). Pass -repin to move the pin on purpose:
#   go run ./cmd/synccostumes -repin
costumes:
	go run ./cmd/synccostumes

# Fail if upstream has costumes the catalog does not know about. For CI or release prep.
# Green when only the pin is behind: that is bookkeeping, not drift.
costumes-check:
	go run ./cmd/synccostumes -check

# Upgrade an existing database to the latest schema (reads env: DB_HOST/DB_USER/DB_PASS/DB_NAME).
# Pass extra flags via ARGS, e.g.: make migrate ARGS="-from v0.1.4a"
migrate:
	go run ./cmd/migrate $(ARGS)

# Show applied vs pending migrations.
migrate-status:
	go run ./cmd/migrate -status

# Build a standalone migrate binary (handy for running on the server).
migrate-build:
	go build -o migrate ./cmd/migrate
