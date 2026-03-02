# Trading Go Platform

A production-ready cryptocurrency trading bot platform built with Go (Fiber) and React.

## Features

- Real-time trading with Binance integration
- Technical analysis indicators (RSI, MACD, Bollinger Bands, SMA, EMA)
- AI-powered trading proposals
- WebSocket live updates
- Position and wallet management
- Backtesting capabilities

## Requirements

- Go 1.21+
- Node.js 18+ (for frontend build)
- SQLite3

## Quick Start

### 1. Clone and Setup

```bash
# Clone repository
git clone <repository-url>
cd trading-go

# Install Go dependencies
make tidy

# Install frontend dependencies
cd frontend && npm install
```

### 2. Environment Configuration

Copy the example environment file and configure:

```bash
cp .env.example .env
```

Edit `.env` with your settings (see Environment Variables section).

### 3. Build and Run

**Development:**
```bash
make run
```

**Production:**
```bash
make build-all
make run-prod
```

The server runs on `http://localhost:5001` by default.

## Makefile Commands

| Command | Description |
|---------|-------------|
| `make run` | Run development server |
| `make build` | Build Go binary |
| `make build-front` | Build React frontend |
| `make build-all` | Build frontend + Go binary |
| `make production` | Production build with optimizations |
| `make run-prod` | Run production binary |
| `make test` | Run tests |
| `make clean` | Clean build artifacts |
| `make docker-build` | Build Docker image |
| `make docker-run` | Run Docker container |

## API Endpoints

### Health & Config
- `GET /api/health` - Health check
- `GET /api/config` - Server configuration

### Wallet
- `GET /api/wallet` - Get wallet balance
- `PUT /api/wallet` - Update wallet

### Positions
- `GET /api/positions` - List all positions
- `POST /api/positions` - Create position
- `POST /api/positions/:id/close` - Close position
- `DELETE /api/positions/:symbol` - Delete position

### Orders
- `GET /api/orders` - List orders
- `POST /api/orders` - Create order

### Trading
- `POST /api/trading/buy` - Execute buy order
- `POST /api/trading/sell` - Execute sell order
- `POST /api/trading/update-prices` - Update prices

### Analysis
- `GET /api/analysis/:symbol` - Get analysis for symbol
- `GET /api/analysis` - Get analysis for default symbol
- `POST /api/analysis/analyze` - Analyze symbol

### AI Trading
- `GET /api/ai/proposals` - List AI proposals
- `POST /api/ai/generate-proposals` - Generate new proposals
- `POST /api/ai/proposals/:id/approve` - Approve proposal
- `POST /api/ai/proposals/:id/deny` - Deny proposal

### Settings
- `GET /api/settings` - Get all settings
- `PUT /api/settings` - Update settings
- `GET /api/settings/:key` - Get single setting
- `GET /api/indicator-weights` - Get indicator weights
- `PUT /api/indicator-weights` - Update indicator weights

## Volatility-Based Position Sizing

When `vol_sizing_enabled` is true, position sizing uses ATR-based risk budgeting and stores per-position exits.

Key settings:
- `vol_sizing_enabled`: Enable volatility-adjusted sizing for auto-trades
- `risk_per_trade`: Risk budget as a percent of portfolio value
- `stop_mult`: ATR multiplier for stop-loss distance
- `tp_mult`: ATR multiplier for take-profit distance
- `max_position_value`: Optional cap on position value (0 disables)
- `time_stop_bars`: Optional time stop in 15m bars when PnL is not positive

### WebSocket
- `WS /ws` - Real-time updates

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `5001` | Server port |
| `DATABASE_PATH` | `./trading.db` | SQLite database path |
| `SECRET_KEY` | `default-secret-key` | Secret key for sessions |
| `DEFAULT_BALANCE` | `400` | Default wallet balance |
| `DEFAULT_CURRENCY` | `USDT` | Default trading currency |
| `BINANCE_API_KEY` | - | Binance API key |
| `BINANCE_SECRET` | - | Binance API secret |
| `REDIS_ADDR` | `localhost:6379` | Redis address |
| `REDIS_PASSWORD` | - | Redis password |
| `REDIS_DB` | `0` | Redis database number |

## Docker Deployment

```bash
# Build image
make docker-build

# Run container
make docker-run
```

Or manually:

```bash
docker build -t trading-go:latest .
docker run -d -p 5001:5001 --env-file .env trading-go:latest
```

## Project Structure

```
trading-go/
├── cmd/server/          # Application entry point
├── internal/
│   ├── config/         # Configuration management
│   ├── cron/           # Scheduled tasks
│   ├── database/       # Database models and initialization
│   ├── handlers/       # HTTP handlers
│   ├── middleware/     # CORS, logging middleware
│   ├── services/       # Business logic (trading, AI, analysis)
│   └── websocket/      # WebSocket hub and client management
├── frontend/           # React frontend
│   ├── src/            # React components
│   └── dist/           # Built frontend assets
├── Makefile            # Build automation
└── README.md           # This file
```

## License

MIT
