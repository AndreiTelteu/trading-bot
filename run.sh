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
go build -ldflags="-s -w" -o build/trading-go cmd/server/main.go

echo "Starting trading server..."
exec ./build/trading-go
