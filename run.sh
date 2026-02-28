#!/bin/bash
set -e

cd "$(dirname "$0")"

echo "Installing frontend dependencies..."
cd frontend
/usr/local/bin/bun install

echo "Building frontend..."
/usr/local/bin/bun run build

cd ..

echo "Building Go backend..."
export CGO_ENABLED=0
go build -ldflags="-s -w" -o trading-go cmd/server/main.go

echo "Starting trading server..."
./trading-go
