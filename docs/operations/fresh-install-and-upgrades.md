# Fresh install and supported upgrades

## Prerequisites

Install PostgreSQL 16, the Go toolchain used by this repository, and Bun (the frontend lockfile is `frontend/bun.lock`). Copy `.env.example` to `.env`, replace authentication/database secrets, and keep all Stage 08 flags at their safe defaults.

Validate compose without starting services:

```bash
docker compose -f docker-compose.yml config
```

For a fresh database, create an empty PostgreSQL database, set `DATABASE_URL`, then start the server. The server applies migrations, verifies the safe Stage 08 authority envelope, and then seeds opening capital through the Stage 01 ledger contract.

```bash
export DATABASE_URL='postgres://USER:PASSWORD@HOST:5432/trading_bot?sslmode=require'
export AUTH_USERNAME='operator'
export AUTH_PASSWORD='REPLACE_ME'
export GOVERNANCE_ADMIN_USERS='operator'
/home/andrei/.local/opt/go-v1.26.1/bin/go run ./cmd/server
```

Successful startup reaches `Server starting on :5001`; any malformed flag, unreconciled requested ledger authority, or invalid Stage 07 live deployment exits before cron, execution workers, or the listener starts.

## Upgrades from Stages 03–07

1. Keep `STAGE08_LEDGER_AUTHORITY=legacy`, all new paths `off`, and take/verify an isolated backup using `backup-restore.md`.
2. Stop the application process so one migrator owns the upgrade.
3. Start the new binary once. Migrations are additive and preserve immutable Stage 01/04/07 records.
4. Authenticate and inspect `GET /api/operations/status`.
5. Reconcile with `go run ./cmd/ledger -action reconcile -json`; exit 0 means balanced, exit 2 means operator action is required.
6. Proceed only through `feature-flags-and-cutover.md`.

Never delete `schema_migrations`, immutable history, or legacy paths during Stage 08. If startup fails, restore the previous binary and safe flag envelope; schema additions are backward-compatible. A database restore is reserved for verified corruption/disaster, not ordinary application rollback.
