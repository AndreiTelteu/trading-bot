#!/bin/bash
set -e

cd "$(dirname "$0")"

echo "Installing frontend dependencies..."
cd frontend
bun install

echo "Building frontend..."
bun run build

cd ..

echo "Building Go backend..."
export CGO_ENABLED=0
export GOOS=linux
export AUTH_USERNAME=admin
export AUTH_PASSWORD=qwe321
go build -ldflags="-s -w" -o build/trading-go cmd/server/main.go

echo "Starting trading server..."
export DATABASE_URL="${DATABASE_URL:-postgres://postgres:postgres@localhost:5432/trading_bot?sslmode=disable}"
./build/trading-go
