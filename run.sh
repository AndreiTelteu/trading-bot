#!/bin/bash
set -e

cd "$(dirname "$0")"

echo "Installing frontend dependencies..."
cd frontend
npm install

echo "Building frontend..."
npm run build

cd ..

echo "Building Go backend with CGO..."
export CGO_ENABLED=1
go build -ldflags="-s -w" -o trading-go cmd/server/main.go

echo "Starting trading server..."
./trading-go
