.PHONY: run build test clean tidy install build-front production run-prod build-all docker-build docker-run

# Development
run:
	go run cmd/server/main.go

build:
	go build -o bin/trading-go cmd/server/main.go

test:
	go test -v ./...

tidy:
	go mod tidy

clean:
	rm -rf bin/
	rm -f trading.db

install:
	go install

# Frontend
build-front:
	cd frontend && npm run build

# Production builds
production:
	CGO_ENABLED=1 GOOS=linux go build -ldflags="-s -w" -o bin/trading-go cmd/server/main.go

build-all: build-front production

# Run production binary
run-prod:
	./bin/trading-go

# Docker
docker-build:
	docker build -t trading-go:latest .

docker-run:
	docker run -d -p 5001:5001 --env-file .env trading-go:latest
