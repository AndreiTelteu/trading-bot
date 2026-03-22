FROM oven/bun:1 AS frontend-builder

WORKDIR /app/frontend

COPY frontend/package.json frontend/bun.lock ./
RUN bun install --frozen-lockfile

COPY frontend/ ./
RUN bun run build

FROM golang:1.21-bookworm AS backend-builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . ./
COPY --from=frontend-builder /app/frontend/dist ./frontend/dist

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/trading-go ./cmd/server/main.go

FROM debian:bookworm-slim

WORKDIR /app

RUN apt-get update \
	&& apt-get install -y --no-install-recommends ca-certificates tzdata \
	&& rm -rf /var/lib/apt/lists/*

COPY --from=backend-builder /out/trading-go /app/trading-go
COPY --from=backend-builder /app/frontend/dist /app/frontend/dist

EXPOSE 5001

ENV PORT=5001

ENTRYPOINT ["/app/trading-go"]
