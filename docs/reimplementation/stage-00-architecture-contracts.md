# Stage 00 — Architecture Contracts and Characterization Harness

## Objective

Create a safe refactoring boundary before changing trading behavior. Capture what the system currently does, define target contracts, and introduce deterministic seams so later stages can be implemented without guessing or silently changing semantics.

## Scope

### Current-state characterization

- [x] Document the live flow from universe selection through analysis, entry qualification, sizing, execution, persistence, and broadcast.
- [x] Document the backtest flow and list every known parity difference.
- [x] Document exit precedence, including hard stop, take profit, trailing stop, signal exit, time stop, and end-of-period liquidation.
- [x] Document governance semantics for `research_only`, `shadow`, `paper`, `limited_live`, `full_live`, and rollback.
- [x] Record accounting invariants and explicitly mark current historical account metrics as unreconciled.

### Target contracts

Define compile-time contracts without migrating all callers yet:

- [x] `Clock`: current time and deterministic test time.
- [x] `IDGenerator`: deterministic IDs for tests and unique IDs in production.
- [x] `MarketDataSource`: bars, quote/ticker, benchmark data, and coverage metadata.
- [x] `UniverseProvider`: point-in-time candidates and membership provenance.
- [x] `Strategy`: pure decision over immutable context.
- [x] `RiskEngine`: converts/rejects intents using portfolio and policy state.
- [x] `Broker`: accepts an approved intent and returns fills/rejections.
- [x] `Ledger`: appends immutable events and exposes reconciliation.

Contracts must avoid importing HTTP handlers, global WebSocket state, or mutable global settings.

### Characterization tests

- [x] Add golden fixtures for indicator scoring and signal classification, including clamp boundaries.
- [x] Add characterization tests for rule-based entry qualification and exact rejection reason ordering.
- [x] Add characterization tests for model rollout behavior.
- [x] Add sizing fixtures for fixed, rebuy, pyramid, and ATR/volatility sizing.
- [x] Add exit precedence fixtures when several exit conditions are simultaneously true.
- [x] Add handler/service tests proving current direct close/delete behavior before Stage 01 changes it.
- [x] Add a test helper that supplies a fixed clock, deterministic IDs, settings, market observations, and portfolio state.

## Implementation guidance

- Prefer new small packages under `internal/tradingcore` or an equivalently explicit namespace.
- Keep domain types free of GORM and Fiber annotations where practical.
- Use adapters at package boundaries rather than moving all existing code immediately.
- Characterization tests must describe existing behavior even when that behavior is later scheduled for replacement.
- Mark known defects in test names or comments; do not normalize them into desired behavior during this stage.
- Do not change trading settings, start backtests, mutate the running database, or deploy runtime changes.

## Testing instructions

### Must be testable now

- [x] New contract packages compile without cyclic imports.
- [x] Fixed-clock and deterministic-ID helpers are reproducible.
- [x] Characterization fixtures pass repeatedly.
- [x] Existing service and backtest tests still pass.
- [x] Full `go test ./...` passes.
- [x] Git diff contains no runtime behavior change except dependency injection seams proven equivalent by tests.

### Cannot yet be proven in this stage

- [ ] Full live/backtest parity cannot be proven until Stage 02 routes both through the shared orchestrator.
- [ ] Ledger reconciliation cannot be proven until Stage 01 introduces immutable events.
- [ ] Historical no-lookahead correctness cannot be proven until Stages 03 and 04.
- [ ] Profitability and strategy quality are explicitly out of scope.

## Acceptance criteria

- [x] Target contracts and package dependency direction are documented.
- [x] Current behavior has regression fixtures around every high-risk branch.
- [x] Later agents can implement against stable interfaces rather than editing handlers directly.
- [x] No current production path is removed.
- [x] Independent reviewer confirms that tests characterize behavior rather than assert implementation details unnecessarily.
