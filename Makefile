.PHONY: dev build run clean

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
	rm -f hailsDotGO static/js/*.js
