## ADDED Requirements

### Requirement: Runtime database configuration uses PostgreSQL
The application SHALL initialize its runtime database connection from PostgreSQL configuration and MUST no longer require a SQLite file path for normal operation.

#### Scenario: DATABASE_URL is provided
- **WHEN** the server starts with `DATABASE_URL` configured
- **THEN** the database layer MUST connect using that PostgreSQL DSN and ignore SQLite-specific configuration

#### Scenario: DATABASE_URL is absent but discrete PostgreSQL settings are present
- **WHEN** the server starts with `POSTGRES_HOST`, `POSTGRES_PORT`, `POSTGRES_DB`, `POSTGRES_USER`, and `POSTGRES_PASSWORD`
- **THEN** the application MUST assemble a valid PostgreSQL DSN and use it for startup

#### Scenario: PostgreSQL configuration is missing or invalid
- **WHEN** the server cannot resolve a usable PostgreSQL connection configuration
- **THEN** startup MUST fail with an actionable configuration error before serving API or cron workloads

### Requirement: PostgreSQL connections are pooled and validated
The application SHALL configure and reuse a shared PostgreSQL connection pool for API handlers, websocket bootstrap queries, cron jobs, backtests, and background services.

#### Scenario: Server startup configures pool limits
- **WHEN** the database layer is initialized
- **THEN** it MUST set explicit open, idle, and lifetime limits on the underlying PostgreSQL connection pool

#### Scenario: Database readiness is checked on startup
- **WHEN** the application finishes creating the GORM database handle
- **THEN** it MUST verify PostgreSQL connectivity before reporting successful initialization

### Requirement: Schema bootstrap is versioned and repeatable
The system SHALL apply versioned PostgreSQL schema migrations for every persisted model before seed data or request handling begins.

#### Scenario: Fresh PostgreSQL database
- **WHEN** the application starts against an empty PostgreSQL database
- **THEN** it MUST create the required schema objects for wallet, positions, orders, settings, AI proposals, indicator weights, LLM config, activity logs, backtest jobs, trend analysis history, and portfolio snapshots

#### Scenario: Existing PostgreSQL database with pending migrations
- **WHEN** the application starts against an existing PostgreSQL database that is behind the current schema version
- **THEN** it MUST apply pending migrations in order before serving traffic

#### Scenario: Seed data after migrations
- **WHEN** migrations complete successfully
- **THEN** default wallet, settings, indicator weights, and LLM config seed records MUST be created only when their target records are missing

### Requirement: All persistence-backed workflows operate on PostgreSQL
Every database-backed handler, service, websocket bootstrap path, cron task, and backtest job SHALL read and write through the PostgreSQL-backed database handle.

#### Scenario: API write is visible to subsequent reads
- **WHEN** an API endpoint creates or updates persisted state such as wallet, positions, orders, settings, proposals, or activity logs
- **THEN** subsequent API and websocket bootstrap reads MUST return the PostgreSQL-persisted data

#### Scenario: Cron or background job updates persisted state
- **WHEN** scheduled price updates, trending analysis, AI proposal generation, or backtest execution writes database records
- **THEN** those writes MUST persist in PostgreSQL and be visible to request-driven reads in the same deployment

#### Scenario: Tests exercise PostgreSQL behavior
- **WHEN** integration or service tests validate persistence-backed features
- **THEN** they MUST run against PostgreSQL-compatible schema and migration setup rather than SQLite-only helpers

### Requirement: Cutover does not import legacy SQLite data
The PostgreSQL migration SHALL support a clean bootstrap without requiring import of legacy SQLite balances, trades, history, or snapshots.

#### Scenario: Legacy SQLite file exists during cutover
- **WHEN** the new PostgreSQL-backed application starts in an environment that still contains an old SQLite database file
- **THEN** startup MUST not depend on that file and MUST initialize PostgreSQL independently
