## Context

The current backend initializes GORM with `github.com/glebarez/sqlite` in `internal/database/database.go`, loads a file path from `internal/config/config.go`, seeds defaults directly after `AutoMigrate`, and assumes SQLite-backed tests in `internal/testing/integration_test.go`, `internal/services/trading_test.go`, and `internal/services/trending_test.go`. Nearly every backend surface reads or writes through the global `database.DB`, including REST handlers, websocket hydration, AI proposal persistence, backtest jobs, and cron-triggered trading/trending workflows.

This migration is cross-cutting because it changes runtime configuration, container/deployment assets, schema lifecycle, and the validation strategy. The user explicitly does not require migration of existing SQLite state, so the design can optimize for a clean PostgreSQL bootstrap instead of dual-write or historical data conversion.

## Goals / Non-Goals

**Goals:**
- Make PostgreSQL the default and supported runtime database for the API server, websocket bootstrap queries, cron jobs, backtests, and background services.
- Introduce explicit PostgreSQL connection management, including DSN assembly, startup ping, and tuned connection pool settings.
- Replace implicit schema creation with a versioned migration path for all persisted models.
- Provide a complete implementation plan covering all database touchpoints, tests, env/config, Docker assets, and deployment scripts.
- Ship a slim container image and compose topology that runs the app against PostgreSQL.

**Non-Goals:**
- Migrating existing SQLite wallet balances, orders, positions, history, or snapshots into PostgreSQL.
- Supporting SQLite and PostgreSQL in parallel for long-term runtime operation.
- Redesigning domain models or trading logic beyond compatibility changes required by PostgreSQL.
- Rewriting the frontend beyond configuration or behavior changes needed to tolerate an empty freshly seeded database.

## Decisions

### 1. PostgreSQL becomes the only runtime database target
- Decision: replace `DATABASE_PATH`-driven SQLite initialization with PostgreSQL configuration centered on `DATABASE_URL`, with optional discrete env fallbacks (`POSTGRES_HOST`, `POSTGRES_PORT`, `POSTGRES_DB`, `POSTGRES_USER`, `POSTGRES_PASSWORD`, `POSTGRES_SSLMODE`).
- Rationale: a DSN-first approach works cleanly in containers and hosted deployments while preserving explicit override fields for local development.
- Alternatives considered:
  - Keep SQLite as a fallback: rejected because it multiplies test and runtime branches during a cross-cutting migration.
  - Support a generic `DB_DRIVER`: rejected because the user asked specifically for full PostgreSQL migration, not multi-engine support.

### 2. Keep GORM, swap only the driver and initialization contract
- Decision: continue using GORM models and query style, but replace `glebarez/sqlite` with `gorm.io/driver/postgres` and initialize the underlying `sql.DB` pool (`SetMaxOpenConns`, `SetMaxIdleConns`, `SetConnMaxLifetime`, `SetConnMaxIdleTime`).
- Rationale: almost all data access is already expressed through GORM; preserving that layer minimizes query churn while still enabling PostgreSQL pooling and health checks.
- Alternatives considered:
  - Rewrite data access to raw SQL or `sqlc`: rejected because it expands scope far beyond the requested migration.
  - Keep default pool settings only: rejected because cron, API traffic, and websocket hydration will share the same process and need predictable connection behavior.

### 3. Add versioned schema migrations instead of relying only on `AutoMigrate`
- Decision: create a migration package and migration assets that establish the PostgreSQL schema explicitly, with startup applying pending migrations before seed data runs.
- Rationale: `AutoMigrate` is convenient for prototypes, but a driver migration is the right time to make schema evolution observable, repeatable, and rollback-aware.
- Alternatives considered:
  - Continue with only `AutoMigrate`: rejected because it does not provide a durable migration history or controlled rollout path.
  - Hand-run SQL in deployment scripts only: rejected because the app and tests need a consistent bootstrap path.

### 4. Treat the first PostgreSQL deploy as a clean bootstrap
- Decision: initialize PostgreSQL with empty schema + default seed data, without importing prior SQLite state.
- Rationale: this matches the user's requirement and removes the riskiest part of database migration.
- Alternatives considered:
  - One-time export/import tooling: rejected as unnecessary work.
  - Live cutover with dual reads/writes: rejected because there is no need to preserve history.

### 5. Replace SQLite-based tests with PostgreSQL-backed test helpers
- Decision: move service/integration tests to a PostgreSQL test database strategy, using either a dedicated test DSN or containerized ephemeral PostgreSQL during test runs.
- Rationale: validating against PostgreSQL is necessary to catch type, default, index, and migration differences that SQLite masks.
- Alternatives considered:
  - Keep SQLite tests for speed: rejected because they would no longer verify the production database behavior.
  - Skip integration coverage temporarily: rejected because this change touches every persistence path.

### 6. Move container runtime to a slimmer multi-stage image plus explicit Postgres dependency
- Decision: replace the current single-stage Alpine image with a multi-stage build that compiles the Go binary and frontend artifacts separately, then runs on a slim final image with only required runtime assets.
- Rationale: the current Dockerfile ships build tooling into runtime and has no PostgreSQL-aware startup topology.
- Alternatives considered:
  - Keep the current image and only add env vars: rejected because it misses the requested slim image improvement.
  - Move to distroless immediately: possible later, but a Debian/Alpine slim final stage is a safer first step if shell startup or cert tooling is still needed.

## Risks / Trade-offs

- [Model-to-Postgres type mismatches or index differences] → Mitigation: review every model for primary keys, unique indexes, text fields, and timestamp defaults before authoring the initial migration.
- [Implicit SQLite behavior hidden in tests] → Mitigation: convert integration/service tests to PostgreSQL early and keep them in the validator set.
- [Cold-start failures when PostgreSQL is unavailable] → Mitigation: add startup ping with bounded retries in container/dev workflows and fail fast with actionable logs.
- [Connection exhaustion under cron + API concurrency] → Mitigation: configure pool limits explicitly and document expected defaults per environment.
- [Seed logic accidentally overwrites operator-managed values] → Mitigation: keep seed behavior idempotent and only create records when missing.
- [Deployment drift between local compose and hosted environment] → Mitigation: standardize on `DATABASE_URL`/Postgres envs and keep Docker/compose examples aligned with app startup.

## Migration Plan

1. Add PostgreSQL configuration fields, DSN resolution, and pool tuning settings in `internal/config`.
2. Replace SQLite driver usage in `internal/database/database.go` with PostgreSQL initialization, startup ping, pool configuration, and migration runner invocation.
3. Create the initial PostgreSQL schema migration for all current persisted models: wallet, positions, orders, settings, AI proposals, indicator weights, LLM config, activity logs, backtest jobs, trend analysis history, and portfolio snapshots.
4. Keep seed data, but make it run only after migrations succeed and only against empty/missing records.
5. Audit every DB-backed code path (handlers, services, websocket bootstrap, cron scheduler/backtest job persistence) for assumptions that may break on PostgreSQL, such as ordering, uniqueness, transaction boundaries, and empty-database handling.
6. Replace SQLite-based tests with PostgreSQL-backed helpers and update validation commands to exercise migrations plus the affected Go test suites.
7. Update environment files, Dockerfile, docker-compose, Makefile targets, and deployment scripts to provision PostgreSQL and use the slim app image.
8. Cut over by deploying a fresh PostgreSQL instance, letting migrations + seeds initialize it, and accepting that prior SQLite data is discarded.
9. Rollback strategy: if the PostgreSQL rollout fails before go-live, redeploy the previous SQLite-based application image/config; after go-live, rollback means restoring from PostgreSQL backups rather than reusing discarded SQLite state.

## Open Questions

- Which migration framework should implementation standardize on: GORM-managed Go migrations, `gormigrate`, or external SQL migrations such as `golang-migrate`?
- Should automated tests rely on `testcontainers-go`, a compose-managed shared test database, or a CI-provided PostgreSQL service?
- Do we want health/readiness endpoints to verify database connectivity before reporting the app as ready in container orchestration?
