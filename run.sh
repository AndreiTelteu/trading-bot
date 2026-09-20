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
# The runtime image intentionally has no git binary. Resolve the mounted
# checkout identity directly so research jobs still receive an exact revision.
if [ -z "${BACKTEST_CODE_REVISION:-}" ] && [ -f .git/HEAD ]; then
    git_head="$(cat .git/HEAD)"
    case "$git_head" in
        "ref: "*) git_ref="${git_head#ref: }"; [ -f ".git/$git_ref" ] && BACKTEST_CODE_REVISION="$(cat ".git/$git_ref")" ;;
        *) BACKTEST_CODE_REVISION="$git_head" ;;
    esac
fi
if ! [[ "${BACKTEST_CODE_REVISION:-}" =~ ^[0-9a-fA-F]{40}$ ]]; then
    echo "BACKTEST_CODE_REVISION must be an exact 40-character Git revision" >&2
    exit 1
fi
export BACKTEST_CODE_REVISION
# Build the package rather than a single source file; the explicit revision
# above remains authoritative when the minimal image cannot invoke git.
go build -ldflags="-s -w" -o build/trading-go ./cmd/server

echo "Starting trading server..."
exec ./build/trading-go
