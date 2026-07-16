# Stage 02 — Shared Strategy, Risk, and Broker Engine

## Objective

Use one decision, risk, and execution orchestration path in backtest, paper, and live modes. Differences between modes belong in adapters, not duplicated trading rules.

## Core domain contracts

### Immutable decision context

- [x] Market timestamp and decision timestamp.
- [x] Point-in-time bars/features and benchmark/regime inputs.
- [x] Point-in-time universe membership and provenance.
- [x] Portfolio cash, positions, exposure, pending orders, and realized risk state.
- [x] Strategy, model, settings, and policy versions.
- [x] No GORM models, Fiber context, network calls, or global mutable state.

### Strategy result

- [x] `OrderIntent` with side, symbol, requested exposure/quantity semantics, reason, horizon, and metadata.
- [x] Explicit `NoAction` result with reason rather than silent skipping.
- [x] Optional ranking/score fields that are observations, not execution authorization.
- [x] Deterministic output for identical context.

### Risk result

- [x] Approved/rejected intent with machine-readable reason.
- [x] Quantity/notional after caps and lot-size normalization.
- [x] Per-position, portfolio, turnover, cash, and concurrency constraints.
- [x] Policy version and calculation trace sufficient for audit.

### Broker result

- [x] Accepted/rejected order.
- [x] One or more fills with price, quantity, costs, timestamps, and provider IDs.
- [x] Idempotent correlation to order intent.

## Implementation work

### Shared orchestrator

- [x] Build a runner that obtains context, calls strategy, calls risk, submits to broker, appends ledger events, and emits observations.
- [x] Keep broadcasting/HTTP responses outside the pure decision/risk path.
- [x] Inject clock, market data, universe, settings/policy source, broker, ledger, and observer.
- [x] Make each rejection/gate observable without changing the decision.

### Legacy strategy adapter

- [x] Extract current indicator voting and entry rules into `LegacyRuleStrategy`.
- [x] Preserve characterized behavior from Stage 00, including known defects until intentionally replaced in Stage 06.
- [x] Extract exit decisions into the same strategy lifecycle or a clearly shared position-management contract.
- [x] Remove duplicated live/backtest gate ordering after parity fixtures pass.

### Broker adapters

- [x] `BacktestBroker`: deterministic simulation, no wall-clock/network dependency.
- [x] `PaperBroker`: observed market price plus configured cost model, ledger-backed.
- [x] `LiveBroker`: exchange requests and fill ingestion, with idempotency.
- [x] All brokers return the same domain fill types.

### Rollout and model behavior

- [x] Resolve governance once per decision context.
- [x] `research_only` and `shadow` models may log/rank but cannot authorize entries.
- [x] Paper/live model authority follows explicit rollout state.
- [x] Fallback behavior is explicit and tested.

## Testing instructions

### Contract and parity tests

- [x] Run identical fixture context through backtest and paper adapters and compare strategy/risk decisions.
- [x] Verify live adapter request construction from the same approved intent without sending external orders.
- [x] Verify rejection reason ordering is stable.
- [x] Verify fixed clock and IDs yield byte-stable decision traces.
- [x] Verify model shadow predictions do not change selected intents.
- [x] Verify policy/version metadata reaches order, fill, and ledger events.

### Scenario tests

- [x] Insufficient cash.
- [x] Existing position and pyramiding disabled/enabled.
- [x] Maximum positions reached.
- [x] Per-position and total exposure exceeded.
- [x] Concurrent/pending order conflict.
- [x] Broker rejection and partial fill.
- [x] Exit with multiple simultaneous triggers.

### Regression verification

- [x] Existing characterized legacy cases preserve decisions.
- [x] Full `go test ./...` passes.
- [x] No runtime mode calls the old execution path except behind an explicit migration flag.

### Cannot yet be proven

- [ ] Historical point-in-time parity remains limited until Stage 04 supplies correct universe/data.
- [ ] Next-bar and lower-timeframe fill realism is completed in Stage 03.
- [ ] New strategy quality is out of scope until Stages 05–07.
- [ ] Real exchange response edge cases require provider fixtures or controlled testnet evidence.

## Acceptance criteria

- [x] Strategy and risk logic have one implementation across modes.
- [x] Brokers contain execution differences only.
- [x] Ledger is the only economic state mutation path.
- [x] Rollout state, not artifact presence, controls model authority.
- [x] Reviewer confirms package direction has no handler/database/global-state leakage into pure logic.
