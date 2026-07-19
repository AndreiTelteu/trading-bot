# AGENTS.md — Trading Platform Engineering Guide

## Purpose

This repository is a PostgreSQL-backed research and paper-trading platform. Its core contract is not “generate a signal and mutate a wallet”; it is to produce deterministic, auditable decisions through the same strategy and risk engine in backtest, paper, and eventual live modes while preserving exact accounting and point-in-time evidence.

The implementation follows [`roadmap.md`](roadmap.md). Read that file and the relevant stage document in [`docs/reimplementation/`](docs/reimplementation/) before changing behavior.

## Current operational status

- Stages 00–08 are implemented and verified.
- The legacy path remains available only for controlled rollback/cutover compatibility.
- The production candidate remains research/shadow fenced until its validation and rollout evidence authorize promotion.
- Direct live exchange order submission is fenced.
- `auto_trade_enabled` is an authenticated operational paper-entry switch. It does not authorize live execution or replace governance approval.
- Missing data, unreconciled accounting, stale authority, or insufficient evidence must fail closed.

## Non-negotiable invariants

1. **One decision path.** Backtest, paper, and live adapters consume the same strategy decisions, portfolio snapshots, and risk decisions.
2. **One accounting truth.** Cash, fills, fees, capital adjustments, position quantity, cost basis, accumulated fees, and realized P&L reconcile to immutable ledger events.
3. **No direct economic bypass.** Handlers and services must not directly create/delete economic projections or mutate protected wallet/position fields.
4. **Point-in-time inputs.** Historical runs may use only data, symbol metadata, tradability, constraints, and universe membership known at the simulated time.
5. **Determinism.** Replay uses explicit clocks/IDs, versioned manifests, deterministic costs, and explicit fill/exit precedence.
6. **Comparable evidence.** Strategy comparisons use the same exposure, fee, slippage, coverage, and final-position policy.
7. **No false evidence.** Missing/empty data, zero-trade output, unreconciled state, or malformed backup evidence cannot be presented as success.
8. **Human-controlled promotion.** Models and strategies cannot promote themselves to paper/live authority. Bootstrap/test artifacts never control execution.
9. **Least privilege.** Migration, runtime, ledger writer, and parity writer connections stay separate and fail closed when missing or overprivileged.
10. **Reversible cutover.** Stage 08 authority and feature flags remain explicit and persisted; legacy removal waits for successful cutover evidence.

## Runtime topology

- **Backend:** Go, Fiber, GORM.
- **Database:** PostgreSQL 16 only. SQLite is not a supported runtime or integration-test substitute.
- **Frontend:** React + Vite.
- **Realtime:** authenticated WebSocket hub.
- **External data:** Binance public market-data endpoints; private/live submission remains fenced.
- **LLM:** configurable provider used for proposals/research. LLM output is advisory and cannot bypass governance.

Compose services:

- `postgres`: PostgreSQL 16 with persistent volume.
- `bootstrap`: one-shot migrations, grants, role/login provisioning, opening-capital seed, and Stage 08 initialization.
- `app`: long-lived backend/frontend server; never receives the migration/admin DSN.

## PostgreSQL authority boundaries

Four DSNs have different purposes:

| Configuration | Login/purpose | Allowed scope |
|---|---|---|
| `MIGRATION_DATABASE_URL[_FILE]` | administrative bootstrap/isolated restore | migrations, ownership, grants, login provisioning |
| `DATABASE_URL[_FILE]` | `trading_bot_app_runtime` | runtime reads, settings/operational state, narrowly scoped non-economic updates |
| `LEDGER_DATABASE_URL[_FILE]` | `trading_bot_app_ledger` | immutable ledger and transaction-coupled economic projections |
| `PARITY_DATABASE_URL[_FILE]` | `trading_bot_app_parity` | Stage 08 parity evidence only |

Rules:

- Never give the server the migration DSN.
- Never fall back from ledger/parity to runtime.
- Never add broad schema/table DML grants to make a test pass.
- Migration execution belongs to `cmd/bootstrap` or explicit operator/restore workflows.
- Runtime has no direct table DML on `backup_verifications`; verified evidence is written only through `record_verified_backup_evidence`, a pinned `SECURITY DEFINER` function.
- Preserve fixed `search_path`, controlled ownership, revoked `PUBLIC EXECUTE`, FK bindings, and server-side checksum/authority validation on security-definer functions.

See [`docs/database-roles.md`](docs/database-roles.md).

## Package map

- `cmd/server`: authenticated HTTP/WebSocket server and schedulers.
- `cmd/bootstrap`: one-shot fresh-install/migration and application-login provisioning.
- `cmd/backtest`: deterministic replay and Stage 05/06 comparison CLI.
- `cmd/marketdata`: point-in-time metadata/bar ingestion, coverage, manifests, and universe snapshots.
- `cmd/ledger`: reconciliation, approved backfill, corrections, and reversals.
- `cmd/operations`: Stage 08 verify/status/fingerprint/restore/backup evidence.
- `internal/tradingcore`: exact values, contracts, strategy, risk, broker adapters, orchestration, exits, determinism, rollout.
- `internal/ledger`: immutable events, fills, corrections, reconciliation, backfill, projection coupling.
- `internal/backtest`: realistic replay, costs, manifests, baselines, candidate strategy, walk-forward integration.
- `internal/pointintime`: historical metadata, bars, coverage, manifests, and universe membership.
- `internal/validation`: walk-forward validation, bootstrap statistics, promotion evidence, ML diagnostics.
- `internal/governance`: immutable experiments, policies, deployments, approval and rollback state.
- `internal/cutover`: Stage 08 flags, authority, parity, and transition contracts.
- `internal/operations`: bootstrap authority, monitoring, backup fingerprints/evidence, and backfill operations.
- `internal/database`: models, migrations, pool wiring, role provisioning, principal validation, seed boundary.
- `internal/services`: application adapters around analysis, exchange data, execution coordination, models, monitoring, and universe policy.
- `internal/handlers`: authenticated API boundaries; handlers orchestrate services but do not bypass domain invariants.
- `frontend`: React operator interface.

## Change workflow

1. Read `roadmap.md`, the relevant stage plan, and affected runbook.
2. Trace the complete path: UI/API → handler → service/domain → DB role/transaction → evidence/output.
3. Write or update the regression test first when fixing a bug.
4. Make the smallest change that preserves all invariants.
5. Test with real PostgreSQL 16 for migrations, grants, roles, transactions, triggers, functions, RLS, fingerprints, and restore behavior.
6. Build the frontend when frontend code changes.
7. Render Compose when topology/configuration changes.
8. Run `git diff --check`, inspect the full diff, and scan staged/untracked files for secrets.
9. Do not claim completion until the requested artifact has been exercised successfully.

## Settings and governance

- `auto_trade_enabled` is a generic operational enable/kill switch. Accepted values are exactly `true` or `false`.
- It enables automated paper-entry evaluation only; direct live submission remains fenced.
- `universe_analyze_top_n` is an authenticated operational analysis-workload limit. It must be an integer from 1 to 1000 and runtime caps it at the governed `universe_top_k`.
- Strategy, risk, universe, model, rollout, execution, fee/slippage, backtest, and indicator policy keys are authority-affecting and must not be silently mutated through the generic settings endpoint.
- The frontend must send only fields actually edited and must surface non-2xx API responses.
- Promotion and rollback use immutable experiment/deployment/transition records and authenticated operator capabilities.

## Accounting rules

- Use exact decimal primitives from `internal/accounting`/`internal/tradingcore`; do not route economic values through `float64` when exactness is required.
- Every fill, fee, capital adjustment, correction, and reversal has a stable idempotency identity.
- Projection updates and ledger events belong in the same top-level PostgreSQL transaction.
- Reconciliation exit code `2` means unbalanced/degraded, not a successful run.
- Administrative corruption tests may bypass immutable triggers only in tightly scoped test setup; production triggers must not be weakened.

## Backtest and data rules

- Signal time and executable fill time are distinct.
- Missing benchmark, execution, universe, tradability, constraint, or feature coverage is an explicit error.
- Do not substitute current exchange listings for historical universe membership.
- Keep final-position policy explicit (`liquidate` or `mark_to_market`).
- Version and persist code/config/dataset/strategy/policy/cost identities in run manifests.
- A candidate cannot be optimized/promoted merely because nominal return is positive; it must pass predefined out-of-sample and baseline gates.

## Testing

Repository Go toolchain in this environment:

```bash
/home/andrei/.local/opt/go-v1.26.1/bin/go
```

Core verification:

```bash
make test-db-up
TEST_DATABASE_URL='postgres://postgres:<test-password>@127.0.0.1:5433/trading_bot_test?sslmode=disable' \
  /home/andrei/.local/opt/go-v1.26.1/bin/go test -p 1 -count=1 ./...

/home/andrei/.local/opt/go-v1.26.1/bin/go vet ./...
cd frontend && npm run build
cd .. && docker compose config --quiet
git diff --check
```

Use serial PostgreSQL tests when packages share/reset one schema. Run race tests for concurrency-sensitive packages (`internal/ledger`, `internal/services`, `internal/tradingcore`, `internal/operations`, `internal/database`) after meaningful concurrency or pool changes.

## Operations

Runbooks are indexed in [`docs/operations/README.md`](docs/operations/README.md). Important commands:

```bash
go run ./cmd/ledger -action reconcile -json
go run ./cmd/operations -action status
go run ./cmd/operations -action fingerprint
go run ./cmd/marketdata -action coverage ...
go run ./cmd/backtest ...
```

Fresh install/rebuild:

```bash
docker compose config --quiet
docker compose up -d --build
```

A fresh installation seeds exactly one opening-capital ledger event and may report operationally degraded until required market-data, backtest, parity, backup, and elapsed shadow evidence exists.

## Security and secret handling

- Never commit `.env`, DSNs, passwords, API keys, pgpass/service files, dumps, or generated credentials.
- Prefer `*_FILE` variables and local files mode `0600`.
- Do not print secret values in tests, logs, reviews, or completion reports.
- API and WebSocket routes are authenticated. Governance approval additionally requires a user listed in `GOVERNANCE_ADMIN_USERS`.
- Treat external market/LLM responses as untrusted input.
- Public CORS, auth weakening, superuser runtime logins, direct projection writes, or migration access in the server are release blockers.

## Documentation expectations

When behavior changes, update the relevant stage document/runbook plus README/AGENTS if the public architecture or contributor contract changes. Document external limitations honestly: real market-history quality, sufficient sample sizes, elapsed shadow observation, exchange behavior, and operational approval cannot be manufactured by tests.
