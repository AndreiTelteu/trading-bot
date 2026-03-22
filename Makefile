.PHONY: run build test test-db-up test-db-down clean tidy install build-front production run-prod build-all docker-build docker-run

TEST_DATABASE_URL ?= postgres://postgres:postgres@localhost:5433/trading_bot_test?sslmode=disable

# Development
run:
	go run cmd/server/main.go

build:
	go build -o bin/trading-go cmd/server/main.go

test:
	TEST_DATABASE_URL=$(TEST_DATABASE_URL) go test -v ./...

test-db-up:
	docker compose --profile test up -d postgres-test

test-db-down:
	docker compose --profile test down

tidy:
	go mod tidy

clean:
	rm -rf bin/
	rm -rf build/

install:
	go install

# Frontend
build-front:
	cd frontend && npm run build

# Production builds
production:
	CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o bin/trading-go cmd/server/main.go

build-all: build-front production

# Run production binary
run-prod:
	./bin/trading-go

# Docker
docker-build:
	docker build -t trading-go:latest .

docker-run:
	docker compose up -d app postgres
