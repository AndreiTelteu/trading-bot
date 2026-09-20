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
# Build the package, not the single main.go file. Package builds retain the
# vcs.revision metadata required by immutable backtest manifests.
go build -ldflags="-s -w" -o build/trading-go ./cmd/server

echo "Starting trading server..."
exec ./build/trading-go
