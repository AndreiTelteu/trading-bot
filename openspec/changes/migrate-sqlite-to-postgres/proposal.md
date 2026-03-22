## Why

SQLite is currently hard-wired into runtime config, database initialization, tests, and local/container workflows, which blocks reliable multi-process deployments and makes concurrent API plus cron workloads riskier as the system grows. We want PostgreSQL to become the only supported runtime database, with a clear rollout plan that covers the full application surface, while explicitly avoiding any effort to preserve existing SQLite trade or balance history.

## What Changes

- Replace the SQLite-only database bootstrap with PostgreSQL-backed initialization driven by explicit connection settings and pooled connections.
- Add startup-safe schema creation and migration handling for all persisted models used by APIs, services, websocket hydration, backtests, and cron jobs.
- Define a full migration plan for every database touchpoint in handlers, services, cron jobs, tests, env/config loading, container runtime, and operational scripts.
- Add Docker support for PostgreSQL-backed development and deployment, including a slimmer application image and compose wiring.
- **BREAKING** Remove SQLite as the primary runtime database target; existing SQLite files are not migrated into PostgreSQL.
- **BREAKING** Replace `DATABASE_PATH`-style configuration with PostgreSQL connection configuration and defaults.

## Capabilities

### New Capabilities
- `postgres-persistence`: Run the application, cron jobs, and background workflows against PostgreSQL with pooled connections, schema bootstrap, and clean startup behavior.
- `postgres-runtime-deployment`: Run the project in containers with a slim application image and PostgreSQL service wiring suitable for local and hosted deployments.

### Modified Capabilities
- None.

## Impact

- Affected backend areas: `internal/config`, `internal/database`, all DB-backed handlers and services, cron scheduler flows, websocket bootstrap queries, and backtest persistence.
- Affected tests: integration tests and service tests that currently use in-memory SQLite.
- Affected ops/runtime assets: `.env.example`, `Dockerfile`, `docker-compose.yml`, `Makefile`, and any deployment scripts that assume a local SQLite file.
- Dependencies: add PostgreSQL GORM driver and migration tooling or structured migration support; remove SQLite runtime dependency once the rollout is complete.
