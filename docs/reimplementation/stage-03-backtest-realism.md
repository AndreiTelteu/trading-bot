# Stage 03 — Deterministic and Realistic Backtesting

## Objective

Turn the backtest into an explicit simulation of information availability and execution. Missing required data must fail the run; it must never produce a misleading completed zero-trade result.

## Coverage contract

- [ ] Define required intervals for decision bars, execution bars, benchmark data, universe data, and model features.
- [ ] Validate minimum/maximum timestamps, gaps, duplicate bars, monotonicity, and member counts before simulation.
- [ ] Fail replay mode when snapshots or members are empty for the requested interval.
- [ ] Distinguish `coverage_failed`, `gating_zero_trades`, `strategy_zero_trades`, and successful runs.
- [ ] Persist coverage diagnostics in the run manifest and compact summary.

## Time and execution semantics

- [ ] Make signal timestamp, decision timestamp, order timestamp, and fill timestamp distinct.
- [ ] For close-derived signals, prohibit filling at the same close unless the policy explicitly models a market-on-close order with defensible data.
- [ ] Default to next executable bar/open or configured lower-resolution execution series.
- [ ] Prevent access to future bars/features through APIs and iteration order.
- [ ] Define deterministic ordering when several symbols are actionable at the same timestamp.

## Cost and fill model

- [ ] Apply fees on each fill, not only at trade summary level.
- [ ] Apply deterministic slippage by side and configured bps/model.
- [ ] Define partial-fill and liquidity-cap policy; if unsupported, reject unsupported configurations clearly.
- [ ] Round quantities/prices according to symbol constraints where metadata is available.
- [ ] Use the shared broker/ledger path from Stages 01–02.
- [ ] Ensure final liquidation uses the same cost and timing rules.

## Regime and benchmark

- [ ] Load BTC or configured benchmark separately from tradable symbols.
- [ ] Reject runs lacking benchmark coverage when strategy/risk requires it.
- [ ] Use only benchmark data available as of each decision timestamp.
- [ ] Record benchmark symbol and coverage in the manifest.

## Reproducibility and artifacts

- [ ] Record code revision, config/policy versions, dataset manifest hash, universe mode, costs, seed, interval, and strategy version.
- [ ] Make repeated runs with identical inputs produce identical fills and metrics.
- [ ] Keep run summaries compact; store large curves/trades in dedicated files/tables rather than duplicating them in giant JSON blobs.
- [ ] Version artifact schemas and validate readers against them.

## Testing instructions

### Coverage tests

- [ ] Empty replay snapshot set returns an explicit error.
- [ ] Snapshot with no members returns an explicit error.
- [ ] Missing benchmark bars returns an explicit error.
- [ ] Internal data gaps above policy tolerance fail or warn according to documented rules.
- [ ] A valid strategy that chooses no trades is distinguished from missing data.

### No-lookahead tests

- [ ] Altering a future bar cannot change an earlier decision.
- [ ] Close-derived signals fill no earlier than the configured executable timestamp.
- [ ] Universe membership effective in the future is invisible earlier.
- [ ] End-of-period handling does not use a later price.

### Determinism and accounting tests

- [ ] Identical input produces identical orders, fills, ledger, equity, and metrics.
- [ ] Fee/slippage examples reconcile exactly.
- [ ] Simultaneous intent ordering remains stable.
- [ ] Final cash plus marked positions equals ledger-derived equity.
- [ ] Full `go test ./...` passes.

### Cannot yet be proven

- [ ] Absence of survivorship bias requires Stage 04 historical asset/universe data.
- [ ] Realistic liquidity/partial fills cannot be asserted without depth/trade data; OHLCV-only limitations must be reported.
- [ ] Profitability remains out of scope.
- [ ] Full strategy comparisons depend on Stage 05.

## Acceptance criteria

- [ ] Zero replay coverage can no longer finish as a neutral completed run.
- [ ] Same-close optimism is removed or explicitly modeled.
- [ ] Costs exist at fill/ledger level.
- [ ] Benchmark is mandatory when required and independent of tradable universe.
- [ ] Reviewer can reproduce a run from its manifest and verify no-lookahead fixtures.
