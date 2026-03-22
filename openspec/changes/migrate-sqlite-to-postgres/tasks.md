## 1. Database configuration and dependencies

- [x] 1.1 Replace SQLite dependencies in `go.mod`/`go.sum` with PostgreSQL driver and the chosen migration library.
- [x] 1.2 Extend `internal/config/config.go` and `internal/config/config_test.go` to support `DATABASE_URL`, discrete PostgreSQL settings, and connection-pool tuning values.
- [x] 1.3 Update `.env.example` and any non-doc runtime config assets to expose PostgreSQL-based environment variables instead of `DATABASE_PATH`.

## 2. PostgreSQL bootstrap, pooling, and schema lifecycle

- [x] 2.1 Refactor `internal/database/database.go` to open PostgreSQL via GORM, validate connectivity, and configure the shared `sql.DB` connection pool.
- [x] 2.2 Create the initial PostgreSQL migration set for all persisted models currently auto-migrated by the app.
- [x] 2.3 Wire migration execution into startup so migrations complete before seed data runs.
- [x] 2.4 Keep `SeedData()` idempotent for a fresh PostgreSQL bootstrap and remove any SQLite-specific assumptions.
- [x] 2.5 Review `internal/database/models.go` and related helpers for PostgreSQL-safe column types, indexes, and key behavior.

## 3. Persistence path migration across APIs and services

- [x] 3.1 Audit and update all DB-backed HTTP handlers in `internal/handlers` (`wallet.go`, `positions.go`, `orders.go`, `settings.go`, `activity.go`, `analyzer.go`, `ai.go`, `websocket.go`) for PostgreSQL compatibility and empty-database behavior.
- [x] 3.2 Audit and update all DB-backed services in `internal/services` (`trading.go`, `trending.go`, `analyzer.go`, `ai.go`) for PostgreSQL compatibility, including ordering, uniqueness, and persistence flows.
- [x] 3.3 Audit and update backtest persistence code in `internal/backtest` and shared helpers such as `internal/database/positions.go` to run cleanly on PostgreSQL.
- [x] 3.4 Review any write paths that should become transactional under PostgreSQL to keep wallet, position, order, and proposal updates consistent.

## 4. Scheduler and application startup migration

- [x] 4.1 Verify `cmd/server/main.go` only starts cron workloads after PostgreSQL initialization and migrations succeed.
- [x] 4.2 Audit `internal/cron/scheduler.go` and every cron-triggered workflow to ensure PostgreSQL-backed reads/writes behave correctly during scheduled execution.
- [x] 4.3 Confirm websocket initial-sync queries and background broadcaster updates continue to work against the PostgreSQL-backed store.

## 5. Test migration and validation

- [x] 5.1 Replace SQLite-based setup in `internal/testing/integration_test.go` with PostgreSQL-backed test initialization that runs migrations before tests.
- [x] 5.2 Replace SQLite-based setup in `internal/services/trading_test.go` and `internal/services/trending_test.go` with PostgreSQL-backed fixtures/helpers.
- [x] 5.3 Add or update validator commands so the migration path is covered by Go test runs and startup checks.
- [x] 5.4 Run the relevant Go test suites against PostgreSQL and fix any query, schema, or timing regressions.

## 6. Container, build, and deployment cutover

- [x] 6.1 Replace the current Dockerfile with a slimmer multi-stage app image that excludes build-only tooling from the final runtime layer.
- [x] 6.2 Update `docker-compose.yml` to provision PostgreSQL storage/service wiring and pass PostgreSQL connection settings to the app container.
- [x] 6.3 Update `Makefile`, `run.sh`, and `deploy.sh` so build/run/deploy automation assumes PostgreSQL instead of `trading.db`.
- [x] 6.4 Remove remaining SQLite-specific cleanup, ignore, and runtime assumptions that are no longer needed after the cutover.
