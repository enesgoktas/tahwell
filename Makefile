.PHONY: all build test run clean help

all: test build

build:
	@echo "→ Building Tahwell binary..."
	@go build -o bin/tahwell ./cmd/tahwell

test:
	@echo "→ Running test suite with race detector..."
	@go test -v -race ./...

run: build
	@./bin/tahwell -port 8080 -db tahwell.db

clean:
	@echo "→ Cleaning artifacts..."
	@rm -rf bin/ tahwell.db tahwell.db*

help:
	@echo "Tahwell Development Commands:"
	@echo "  make build   - Compiles the cmd/tahwell executable to bin/"
	@echo "  make test    - Runs all unit & integration tests with race detector"
	@echo "  make run     - Builds and runs the gateway on port 8080"
	@echo "  make clean   - Removes binaries and local database"
