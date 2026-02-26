# Trading Bot - Implementation Plan

## Overview
**Stack:** Flask (Python) + React + SQLite + Docker  
**Deployment:** Linux server with Docker Compose  
**Control:** Web interface only (no OpenClaw dependency)

---

## 1. Project Structure

```
trading-bot/
├── backend/
│   ├── app.py                 # Flask main application
│   ├── config.py              # Configuration
│   ├── models.py              # SQLAlchemy models
│   ├── database.py            # Database initialization
│   ├── routes/
│   │   ├── api.py             # REST API endpoints
│   │   ├── websocket.py       # WebSocket handlers
│   │   └── settings.py       # Settings API
│   ├── services/
│   │   ├── trading.py         # Trading logic
│   │   ├── analyzer.py        # Market analysis
│   │   ├── ai_service.py      # AI analysis & proposals
│   │   └── scheduler.py       # Background job scheduler
│   ├── workers/
│   │   ├── trading_worker.py  # Trading execution queue
│   │   ├── analysis_worker.py # Market analysis queue
│   │   └── ai_worker.py       # AI proposals queue
│   └── utils/
│       ├── indicators.py       # Technical indicators
│       └── binance.py         # Exchange API wrapper
├── frontend/
│   ├── src/
│   │   ├── App.jsx
│   │   ├── components/
│   │   │   ├── Dashboard.jsx
│   │   │   ├── PositionsTable.jsx
│   │   │   ├── PnLChart.jsx
│   │   │   ├── SettingsPanel.jsx
│   │   │   ├── AIProposal.jsx
│   │   │   └── LLMConfig.jsx
│   │   ├── hooks/
│   │   │   └── useWebSocket.js
│   │   └── styles/
│   │       └── theme.css
│   └── package.json
├── docker-compose.yml
├── Dockerfile.backend
├── Dockerfile.frontend
└── requirements.txt
```

---

## 2. Database Schema (SQLite)

### Tables

```sql
-- Wallet / Account
CREATE TABLE wallet (
    id INTEGER PRIMARY KEY,
    balance REAL NOT NULL DEFAULT 400.0,
    currency TEXT DEFAULT 'USDT',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Positions
CREATE TABLE positions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    symbol TEXT NOT NULL,
    amount REAL NOT NULL,
    avg_price REAL NOT NULL,
    entry_price REAL,
    current_price REAL,
    pnl REAL DEFAULT 0,
    pnl_percent REAL DEFAULT 0,
    status TEXT DEFAULT 'open', -- open, closed
    opened_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    closed_at TIMESTAMP,
    close_reason TEXT, -- manual, stop_loss, take_profit, sell_signal
    UNIQUE(symbol)
);

-- Order History
CREATE TABLE orders (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    order_type TEXT NOT NULL, -- buy, sell
    symbol TEXT NOT NULL,
    amount_crypto REAL NOT NULL,
    amount_usdt REAL NOT NULL,
    price REAL NOT NULL,
    fee REAL DEFAULT 0,
    executed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Settings (key-value store for all configuration)
CREATE TABLE settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    category TEXT, -- 'trading', 'ai', 'indicators'
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- AI Proposals
CREATE TABLE ai_proposals (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    proposal_type TEXT NOT NULL, -- parameter_change, stop_loss, etc
    parameter_key TEXT,
    old_value TEXT,
    new_value TEXT,
    reasoning TEXT,
    status TEXT DEFAULT 'pending', -- pending, approved, denied
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    resolved_at TIMESTAMP,
    previous_proposal_id INTEGER REFERENCES ai_proposals(id)
);

-- Indicator Weights
CREATE TABLE indicator_weights (
    indicator TEXT PRIMARY KEY,
    weight REAL NOT NULL DEFAULT 1.0
);
```

---

## 3. Core Features

### 3.1 Trading Settings (Web Configurable)

| Setting | Type | Default | Description |
|---------|------|---------|-------------|
| `entry_percent` | float | 5.0 | % of balance per trade |
| `stop_loss_percent` | float | 5.0 | Exit at X% loss |
| `take_profit_percent` | float | 30.0 | Exit at X% profit |
| `rebuy_percent` | float | 2.5 | % for scaling position |
| `max_positions` | int | 5 | Max concurrent positions |
| `buy_only_strong` | bool | true | Buy only on STRONG_BUY |
| `sell_on_signal` | bool | true | Auto-sell on SELL signal |
| `min_confidence_to_buy` | float | 4.0 | Minimum rating to execute buy |
| `min_confidence_to_sell` | float | 3.5 | Minimum rating to execute sell |
| `allow_sell_at_loss` | bool | false | Allow selling at a loss |
| `trailing_stop_enabled` | bool | false | Enable trailing stop |
| `trailing_stop_percent` | float | 10.0 | % below max price for trailing stop |
| `pyramiding_enabled` | bool | false | Allow adding to winning positions |
| `max_pyramid_layers` | int | 3 | Max rebuy layers per position |
| `position_scale_percent` | float | 50.0 | % reduction per pyramid layer |

### 3.2 AI Settings

| Setting | Type | Default | Description |
|---------|------|---------|-------------|
| `ai_analysis_interval` | int | 24 | Hours between AI analysis runs |
| `ai_lookback_days` | int | 30 | Days of history for AI to analyze |
| `ai_min_proposals` | int | 1 | Minimum proposals per analysis |
| `ai_auto_apply_days` | int | 0 | Auto-apply after X days (0=disabled) |

### 3.3 Indicator Parameters

| Indicator | Parameter | Default | Configurable |
|-----------|-----------|---------|--------------|
| MACD | `macd_fast_period` | 12 | Yes |
| MACD | `macd_slow_period` | 26 | Yes |
| MACD | `macd_signal_period` | 9 | Yes |
| RSI | `rsi_period` | 14 | Yes |
| RSI | `rsi_oversold` | 30 | Yes |
| RSI | `rsi_overbought` | 70 | Yes |
| Bollinger Bands | `bb_period` | 20 | Yes |
| Bollinger Bands | `bb_std` | 2.0 | Yes |
| Volume | `volume_ma_period` | 20 | Yes |
| Momentum | `momentum_period` | 10 | Yes |

### 3.4 Indicator Weights

| Indicator | Default Weight | Configurable |
|-----------|----------------|---------------|
| MACD | 1.0 | Yes |
| RSI | 1.0 | Yes |
| Bollinger Bands | 1.0 | Yes |
| Volume | 0.5 | Yes |
| Price Momentum | 1.0 | Yes |

### 3.3 Background Queues (Redis/Celery)

- **Trading Queue:** Execute buy/sell orders
- **Analysis Queue:** Run market analysis on schedule
- **AI Queue:** Generate improvement proposals

---

## 4. AI Component

### 4.1 LLM Configuration
```json
{
  "provider": "openrouter",  // or openai, custom
  "base_url": "https://openrouter.ai/api/v1",
  "api_key": "sk-...",
  "model": "google/gemini-2.0-flash-001"
}
```

### 4.2 AI Capabilities
1. **Query History:** Full access to orders, positions, P&L, reasons
2. **Analyze Period:** Configurable (7d, 30d, 90d)
3. **Propose Changes:** Modify settings based on analysis
4. **Track Proposals:** Store all proposals with approve/deny history

### 4.3 Proposal Flow
1. AI analyzes last X days of trading
2. Generates parameter change proposal
3. Displays in UI with reasoning
4. User clicks Approve/Deny
5. If approved, applies change and logs

---

## 5. Web Interface

### 5.1 Dashboard
- Total portfolio value (USDT)
- Daily P&L
- Open positions table with live prices
- P&L chart (line graph)

### 5.2 Positions Table
- Symbol, Amount, Avg Price, Current Price
- P&L (absolute + %)
- Status badge
- Close button (manual)

### 5.3 Settings Panel
- All trading settings (input fields)
- Indicator weights (sliders)
- Save/Apply buttons

### 5.4 AI Proposals
- List of pending proposals
- Reasoning text
- Approve/Deny buttons
- History of past proposals

### 5.5 LLM Config
- Provider selector
- Base URL input
- API Key (masked)
- Model selector
- Test connection button

---

## 6. WebSocket Events

| Event | Payload | Description |
|-------|---------|-------------|
| `position_update` | {symbol, price, pnl} | Live price update |
| `order_executed` | {order details} | Trade completed |
| `balance_update` | {new_balance} | Balance changed |
| `ai_proposal` | {proposal details} | New AI proposal |

---

## 7. Docker Deployment

### docker-compose.yml
```yaml
version: '3.8'

services:
  backend:
    build: .
    ports:
      - "5000:5000"
    environment:
      - FLASK_ENV=production
      - DATABASE_URL=sqlite:///trading.db
    volumes:
      - ./data:/app/data
    depends_on:
      - redis

  frontend:
    build: ./frontend
    ports:
      - "3000:3000"
    depends_on:
      - backend

  redis:
    image: redis:7-alpine
    ports:
      - "6379:6379"

  celery-worker:
    build: .
    command: celery -A workers worker
    volumes:
      - ./data:/app/data
    depends_on:
      - redis
```

---

## 8. Implementation Priority

1. **Phase 1:** Database + Flask API + Basic UI
2. **Phase 2:** Trading logic + WebSocket updates
3. **Phase 3:** Settings panel + Indicator weights
4. **Phase 4:** AI component + Proposals
5. **Phase 5:** Docker + Deployment

---

## 9. API Endpoints

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/wallet` | Get balance |
| GET | `/api/positions` | Get all positions |
| POST | `/api/positions/:symbol/close` | Close position |
| GET | `/api/orders` | Get order history |
| GET | `/api/settings` | Get all settings |
| PUT | `/api/settings` | Update settings |
| GET | `/api/ai/proposals` | Get AI proposals |
| POST | `/api/ai/proposals/:id/approve` | Approve proposal |
| POST | `/api/ai/proposals/:id/deny` | Deny proposal |
| GET | `/api/llm/config` | Get LLM config |
| PUT | `/api/llm/config` | Update LLM config |
| GET | `/ws` | WebSocket endpoint |

---

*Plan generated for trading bot rewrite*
