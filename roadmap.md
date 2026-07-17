# Trading Bot Reimplementation Roadmap

## Mission

Rebuild the trading core into a trustworthy research, paper, and live execution platform. A strategy is promotable only when the same decision and risk code runs in every mode, the account ledger reconciles exactly, historical inputs are point-in-time, costs are represented consistently, and multi-window out-of-sample evidence passes predefined gates.

## Status legend

- `[ ]` not started or not verified
- `[x]` implemented and verified

A stage is checked only after implementation, independent Codex review, one feedback/fix pass in the original implementation session, local verification, commit, and push.

## Non-negotiable principles

- [ ] One strategy decision path for backtest, paper, and live
- [ ] One risk engine for backtest, paper, and live
- [ ] Immutable cash/fill/fee/capital-adjustment ledger
- [ ] Point-in-time market data and universe membership
- [ ] Deterministic backtests with explicit coverage failures
- [ ] Same fee/slippage assumptions when comparing strategies
- [ ] No profitability claim from unreconciled or zero-trade evidence
- [ ] No bootstrap/test model may control paper or live entries
- [ ] Human approval remains required for promotion to live execution
- [ ] Legacy behavior remains behind explicit feature flags until cutover is verified

## Stage 00 — Architecture contracts and characterization harness

Detailed plan: [`docs/reimplementation/stage-00-architecture-contracts.md`](docs/reimplementation/stage-00-architecture-contracts.md)

- [x] Record current architecture, invariants, and known-invalid evidence boundaries
- [x] Add characterization tests around existing entry, exit, sizing, and rollout behavior
- [x] Introduce deterministic clock/ID seams needed by later stages
- [x] Define package contracts for strategy, risk, broker, market data, and ledger
- [x] Preserve current runtime behavior in this stage
- [x] Pass targeted and full repository verification
- [x] Complete independent review and one feedback pass

## Stage 01 — Immutable ledger and reconciliation

Detailed plan: [`docs/reimplementation/stage-01-immutable-ledger.md`](docs/reimplementation/stage-01-immutable-ledger.md)

- [x] Add immutable ledger schema and typed entry model
- [x] Route fills, fees, and capital adjustments through atomic ledger transactions
- [x] Derive/reconcile wallet and position projections from ledger events
- [x] Eliminate or fence direct close/delete paths that bypass accounting
- [x] Add reconciliation reports and migration/backfill controls
- [x] Add paper fee/slippage accounting
- [x] Pass invariant, concurrency, rollback, API, and migration tests
- [x] Complete independent review and one feedback pass

## Stage 02 — Shared strategy, risk, and broker engine

Detailed plan: [`docs/reimplementation/stage-02-shared-engine.md`](docs/reimplementation/stage-02-shared-engine.md)

- [x] Introduce common strategy decision and order-intent contracts
- [x] Introduce a shared portfolio risk engine and explicit rejection reasons
- [x] Add broker adapters for backtest, paper, and live
- [x] Extract legacy rules behind a `LegacyRuleStrategy`
- [x] Route all execution modes through one orchestrator
- [x] Honor rollout/fallback state consistently
- [x] Add fixture-based parity and deterministic replay tests
- [x] Complete independent review and one feedback pass

## Stage 03 — Deterministic and realistic backtesting

Detailed plan: [`docs/reimplementation/stage-03-backtest-realism.md`](docs/reimplementation/stage-03-backtest-realism.md)

- [x] Fail fast on missing/empty replay and benchmark coverage
- [x] Separate signal time from next executable fill time
- [x] Support lower-resolution execution data when configured
- [x] Apply deterministic fees, slippage, partial-fill policy, and exit precedence
- [x] Require benchmark/regime data independently of tradable symbols
- [x] Produce compact, versioned, auditable run manifests
- [x] Add no-lookahead, coverage, determinism, and parity tests
- [x] Complete independent review and one feedback pass

## Stage 04 — Point-in-time market data and universe

Detailed plan: [`docs/reimplementation/stage-04-point-in-time-data.md`](docs/reimplementation/stage-04-point-in-time-data.md)

- [x] Model listing, delisting, liquidity, and universe membership by effective time
- [x] Build dataset coverage manifests and validation commands
- [x] Add idempotent historical universe snapshot generation/backfill
- [x] Remove current-universe and listing-age leakage from historical runs
- [x] Guarantee benchmark availability without making it tradable
- [x] Add point-in-time and survivorship-bias regression fixtures
- [x] Document external data limitations explicitly
- [x] Complete independent review and one feedback pass

## Stage 05 — Benchmarks and simple strategy baselines

Detailed plan: [`docs/reimplementation/stage-05-benchmarks-baselines.md`](docs/reimplementation/stage-05-benchmarks-baselines.md)

- [ ] Add cash and buy-and-hold benchmarks
- [ ] Add BTC trend and equal-weight liquid-universe baselines
- [ ] Add a simple cross-sectional momentum baseline
- [ ] Compare all strategies at equal exposure and costs
- [ ] Report excess return, turnover, concentration, and regime cohorts
- [ ] Add synthetic-series and golden-metrics tests
- [ ] Block optimization when a candidate does not beat its baseline robustly
- [ ] Complete independent review and one feedback pass

## Stage 06 — Production candidate: trend/momentum with explicit risk

Detailed plan: [`docs/reimplementation/stage-06-trend-momentum-strategy.md`](docs/reimplementation/stage-06-trend-momentum-strategy.md)

- [ ] Implement one documented, falsifiable trend/momentum hypothesis
- [ ] Add regime gating, liquidity filters, ranking, and controlled rebalance cadence
- [ ] Add per-position and total-exposure caps
- [ ] Add turnover limits and explicit exit semantics
- [ ] Add component ablations and sensitivity reports
- [ ] Keep the strategy research/shadow-only until validation passes
- [ ] Add unit, scenario, parity, and regression tests
- [ ] Complete independent review and one feedback pass

## Stage 07 — Validation, governance, and ML quarantine

Detailed plan: [`docs/reimplementation/stage-07-validation-governance.md`](docs/reimplementation/stage-07-validation-governance.md)

- [ ] Add purged multi-window walk-forward validation
- [ ] Bootstrap the correct statistical unit and reject insufficient samples
- [ ] Define immutable experiment manifests and promotion gates
- [ ] Add calibration/ranking diagnostics for shadow models
- [ ] Prevent bootstrap/test artifacts from controlling execution
- [ ] Require rollback criteria and human approval for promotion
- [ ] Add validation, governance, and negative-path tests
- [ ] Complete independent review and one feedback pass

## Stage 08 — Migration, cutover, and operations

Detailed plan: [`docs/reimplementation/stage-08-migration-cutover.md`](docs/reimplementation/stage-08-migration-cutover.md)

- [ ] Add feature flags and reversible migration sequencing
- [ ] Run old/new decision paths in shadow comparison mode
- [ ] Expose reconciliation, parity, coverage, and validation status operationally
- [ ] Provide migration, rollback, and incident runbooks
- [ ] Gate legacy removal on explicit acceptance criteria
- [ ] Verify backend, frontend, Compose, migrations, and restore path
- [ ] Remove/deprecate legacy paths only after successful cutover
- [ ] Complete independent review and one feedback pass

## Final roadmap audit

- [ ] Reconcile every roadmap item with implemented code and tests
- [ ] Identify partial, missing, duplicated, or obsolete work
- [ ] Run one final Codex implementation session for verified gaps
- [ ] Independently verify the final gap-closing diff
- [ ] Run full backend, frontend, migration, and Compose checks
- [ ] Confirm repository clean and pushed
- [ ] Record final limitations that require real market history or elapsed shadow time

## Global definition of done

- [ ] `go test ./...` passes using the repository Go toolchain
- [ ] Frontend build/typecheck passes when frontend code changes
- [ ] Compose configuration validates when runtime topology changes
- [ ] Database migrations work both on a fresh database and an upgraded fixture
- [ ] No direct mutation path can bypass fills/fees/capital adjustments
- [ ] Backtest, paper, and live produce the same decision for the same fixture state
- [ ] Missing data fails explicitly instead of yielding neutral metrics
- [ ] Documentation identifies anything that cannot be validated without external data, sufficient history, or elapsed shadow operation
