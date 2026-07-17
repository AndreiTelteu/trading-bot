# Stage 03 — Deterministic and Realistic Backtesting

## Objective

Turn the backtest into an explicit simulation of information availability and execution. Missing required data must fail the run; it must never produce a misleading completed zero-trade result.

## Coverage contract

- [x] Define required intervals for decision bars, execution bars, benchmark data, universe data, and model features.
- [x] Validate minimum/maximum timestamps, gaps, duplicate bars, monotonicity, and member counts before simulation.
- [x] Fail replay mode when snapshots or members are empty for the requested interval.
- [x] Distinguish `coverage_failed`, `gating_zero_trades`, `strategy_zero_trades`, and successful runs.
- [x] Persist coverage diagnostics in the run manifest and compact summary.

## Time and execution semantics

- [x] Make signal timestamp, decision timestamp, order timestamp, and fill timestamp distinct.
- [x] For close-derived signals, prohibit filling at the same close unless the policy explicitly models a market-on-close order with defensible data.
- [x] Default to next executable bar/open or configured lower-resolution execution series.
- [x] Prevent access to future bars/features through APIs and iteration order.
- [x] Define deterministic ordering when several symbols are actionable at the same timestamp.

## Cost and fill model

- [x] Apply fees on each fill, not only at trade summary level.
- [x] Apply deterministic slippage by side and configured bps/model.
- [x] Define partial-fill and liquidity-cap policy; if unsupported, reject unsupported configurations clearly.
- [x] Round quantities/prices according to symbol constraints where metadata is available.
- [x] Use the shared broker/ledger path from Stages 01–02.
- [x] Ensure final liquidation uses the same cost and timing rules.

## Regime and benchmark

- [x] Load BTC or configured benchmark separately from tradable symbols.
- [x] Reject runs lacking benchmark coverage when strategy/risk requires it.
- [x] Use only benchmark data available as of each decision timestamp.
- [x] Record benchmark symbol and coverage in the manifest.

## Reproducibility and artifacts

- [x] Record code revision, config/policy versions, dataset manifest hash, universe mode, costs, seed, interval, and strategy version.
- [x] Make repeated runs with identical inputs produce identical fills and metrics.
- [x] Keep run summaries compact; store large curves/trades in dedicated files/tables rather than duplicating them in giant JSON blobs.
- [x] Version artifact schemas and validate readers against them.

## Testing instructions

### Coverage tests

- [x] Empty replay snapshot set returns an explicit error.
- [x] Snapshot with no members returns an explicit error.
- [x] Missing benchmark bars returns an explicit error.
- [x] Internal data gaps above policy tolerance fail or warn according to documented rules.
- [x] A valid strategy that chooses no trades is distinguished from missing data.

### No-lookahead tests

- [x] Altering a future bar cannot change an earlier decision.
- [x] Close-derived signals fill no earlier than the configured executable timestamp.
- [x] Universe membership effective in the future is invisible earlier.
- [x] End-of-period handling does not use a later price.

### Determinism and accounting tests

- [x] Identical input produces identical orders, fills, ledger, equity, and metrics.
- [x] Fee/slippage examples reconcile exactly.
- [x] Simultaneous intent ordering remains stable.
- [x] Final cash plus marked positions equals ledger-derived equity.
- [x] Full `go test ./...` passes.

### Cannot yet be proven

- [ ] Absence of survivorship bias requires Stage 04 historical asset/universe data.
- [ ] Realistic liquidity/partial fills cannot be asserted without depth/trade data; OHLCV-only limitations must be reported.
- [ ] Profitability remains out of scope.
- [ ] Full strategy comparisons depend on Stage 05.

## Acceptance criteria

- [x] Zero replay coverage can no longer finish as a neutral completed run.
- [x] Same-close optimism is removed or explicitly modeled.
- [x] Costs exist at fill/ledger level.
- [x] Benchmark is mandatory when required and independent of tradable universe.
- [x] Reviewer can reproduce a run from its manifest and verify no-lookahead fixtures.
