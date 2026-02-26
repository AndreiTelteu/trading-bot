# AGENTS.md - Trading Bot Development Guide

This file provides guidelines for agentic coding agents working on this repository.

## Project Overview

Trading Bot Platform with:
- **Backend**: Python Flask with SQLAlchemy, SocketIO, CCXT for exchange integration
- **Frontend**: React 18 with Vite, Socket.IO client, Recharts

## Project Structure

```
trading-new/
├── backend/
│   ├── app.py              # Flask application entry point
│   ├── config.py           # Configuration settings
│   ├── database.py         # Database initialization
│   ├── models.py           # SQLAlchemy models
│   ├── routes/
│   │   ├── api.py          # REST API endpoints
│   │   ├── settings.py     # Settings management
│   │   └── websocket.py    # WebSocket handlers
│   ├── services/
│   │   ├── analyzer.py     # Technical analysis (RSI, MACD, BB, etc.)
│   │   ├── ai_service.py  # AI/ML integration
│   │   └── trading.py      # Trading execution
│   └── utils/              # Utility modules
├── frontend/
│   ├── src/
│   │   ├── App.jsx         # Main React component
│   │   ├── main.jsx        # Entry point
│   │   └── components/     # React components
│   ├── package.json
│   └── vite.config.js
└── pyproject.toml
```

## Commands

### Backend

```bash
# Run Flask server (development)
cd backend && python -m backend.app

# Or use the run script (builds frontend first)
./run.sh
```

The backend runs on port 5001 by default (`PORT` env var).

### Frontend

```bash
cd frontend

# Development server (port 3000)
npm run dev

# Production build
npm run build

# Preview production build
npm run preview
```

### Testing

**No tests currently configured.** To add tests:

```bash
# Python (pytest)
pip install pytest pytest-flask
pytest backend/ -v

# Run single test
pytest backend/tests/test_file.py::test_name -v

# JavaScript (if added)
npm install vitest
npx vitest run
```

### Linting

```bash
# Python (ruff - recommended)
pip install ruff
ruff check backend/

# JavaScript (ESLint - if added)
cd frontend
npm install eslint
npx eslint src/
```

## Code Style Guidelines

### Python (Backend)

**Imports**:
- Standard library first, then third-party, then local
- Use explicit relative imports: `from backend.models import db`
- Group by: `from flask import` → `from backend.x import` → local imports

**Formatting**:
- 4 spaces for indentation
- Maximum line length: 100 characters
- Use Black for formatting: `black backend/`

**Types**:
- Python 3.12+ with type hints preferred
- Use `typing` module for complex types

**Naming Conventions**:
- Classes: `PascalCase` (e.g., `class Wallet:`)
- Functions/variables: `snake_case` (e.g., `def get_wallet():`)
- Constants: `UPPER_SNAKE_CASE`
- Private methods: prefix with `_` (e.g., `_private_method`)

**Error Handling**:
- Always wrap API route handlers in try/except
- Return proper HTTP status codes: `return jsonify({"error": "message"}), 404`
- Log errors: `import logging; logger = logging.getLogger(__name__)`

**Database Models** (SQLAlchemy):
- Define `__tablename__` explicitly
- Use `db.Column` with proper types
- Set defaults inline: `default=0.0`
- Use `db.relationship` for related models

### JavaScript/React (Frontend)

**Imports**:
- React/core imports first, then third-party, then local components
- Use consistent alias paths if configured

**Formatting**:
- 2 spaces for indentation
- Use Prettier: `npx prettier --write src/`

**Types**:
- TypeScript preferred for new components
- Use PropTypes for existing JSX components

**Naming Conventions**:
- Components: `PascalCase` (e.g., `Dashboard.jsx`)
- Functions/variables: `camelCase`
- Hooks: prefix with `use` (e.g., `useState`, `useEffect`)

**Component Structure**:
```jsx
import React, { useState, useEffect } from 'react'

function ComponentName({ prop1, prop2 }) {
  const [state, setState] = useState(initialValue)

  useEffect(() => {
    // effect logic
    return () => cleanup // optional
  }, [deps])

  // handlers...

  return (
    <div>...</div>
  )
}

export default ComponentName
```

**API Calls**:
- Use `fetch` or axios consistently
- Handle errors with try/catch
- Show user feedback on errors

### General

- No trailing whitespace
- No commit secrets/keys (use `.env` files)
- Keep functions small and focused (single responsibility)
- Add docstrings for complex functions
- Use meaningful variable names

## Environment Variables

Create `.env` in project root:

```bash
# Backend
FLASK_APP=app.py
PORT=5001
DATABASE_URL=sqlite:///trading.db
SECRET_KEY=your-secret-key
BINANCE_API_KEY=your-key
BINANCE_SECRET=your-secret
```

## Common Tasks

### Adding a New API Endpoint

1. Add route in `backend/routes/api.py`
2. Follow existing patterns: `@api.route("/endpoint", methods=["GET"])`
3. Return JSON with `jsonify()`
4. Handle errors with try/except

### Adding a New Frontend Component

1. Create `frontend/src/components/NewComponent.jsx`
2. Import and add to `App.jsx`
3. Use existing components as reference

### Adding a Database Model

1. Add class in `backend/models.py`
2. Define columns with appropriate types
3. Run: `cd backend && python -c "from app import app; from models import db; app.app_context().push(); db.create_all()"`

## Notes

- Backend proxies API requests to Flask on port 5001
- WebSocket communication via Socket.IO
- Frontend hot-reloads in development
- Database is SQLite by default (`trading.db`)
