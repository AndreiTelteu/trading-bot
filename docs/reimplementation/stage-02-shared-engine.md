# Stage 02 — Shared Strategy, Risk, and Broker Engine

## Objective

Use one decision, risk, and execution orchestration path in backtest, paper, and live modes. Differences between modes belong in adapters, not duplicated trading rules.

## Core domain contracts

### Immutable decision context

- [ ] Market timestamp and decision timestamp.
- [ ] Point-in-time bars/features and benchmark/regime inputs.
- [ ] Point-in-time universe membership and provenance.
- [ ] Portfolio cash, positions, exposure, pending orders, and realized risk state.
- [ ] Strategy, model, settings, and policy versions.
- [ ] No GORM models, Fiber context, network calls, or global mutable state.

### Strategy result

- [ ] `OrderIntent` with side, symbol, requested exposure/quantity semantics, reason, horizon, and metadata.
- [ ] Explicit `NoAction` result with reason rather than silent skipping.
- [ ] Optional ranking/score fields that are observations, not execution authorization.
- [ ] Deterministic output for identical context.

### Risk result

- [ ] Approved/rejected intent with machine-readable reason.
- [ ] Quantity/notional after caps and lot-size normalization.
- [ ] Per-position, portfolio, turnover, cash, and concurrency constraints.
- [ ] Policy version and calculation trace sufficient for audit.

### Broker result

- [ ] Accepted/rejected order.
- [ ] One or more fills with price, quantity, costs, timestamps, and provider IDs.
- [ ] Idempotent correlation to order intent.

## Implementation work

### Shared orchestrator

- [ ] Build a runner that obtains context, calls strategy, calls risk, submits to broker, appends ledger events, and emits observations.
- [ ] Keep broadcasting/HTTP responses outside the pure decision/risk path.
- [ ] Inject clock, market data, universe, settings/policy source, broker, ledger, and observer.
- [ ] Make each rejection/gate observable without changing the decision.

### Legacy strategy adapter

- [ ] Extract current indicator voting and entry rules into `LegacyRuleStrategy`.
- [ ] Preserve characterized behavior from Stage 00, including known defects until intentionally replaced in Stage 06.
- [ ] Extract exit decisions into the same strategy lifecycle or a clearly shared position-management contract.
- [ ] Remove duplicated live/backtest gate ordering after parity fixtures pass.

### Broker adapters

- [ ] `BacktestBroker`: deterministic simulation, no wall-clock/network dependency.
- [ ] `PaperBroker`: observed market price plus configured cost model, ledger-backed.
- [ ] `LiveBroker`: exchange requests and fill ingestion, with idempotency.
- [ ] All brokers return the same domain fill types.

### Rollout and model behavior

- [ ] Resolve governance once per decision context.
- [ ] `research_only` and `shadow` models may log/rank but cannot authorize entries.
- [ ] Paper/live model authority follows explicit rollout state.
- [ ] Fallback behavior is explicit and tested.

## Testing instructions

### Contract and parity tests

- [ ] Run identical fixture context through backtest and paper adapters and compare strategy/risk decisions.
- [ ] Verify live adapter request construction from the same approved intent without sending external orders.
- [ ] Verify rejection reason ordering is stable.
- [ ] Verify fixed clock and IDs yield byte-stable decision traces.
- [ ] Verify model shadow predictions do not change selected intents.
- [ ] Verify policy/version metadata reaches order, fill, and ledger events.

### Scenario tests

- [ ] Insufficient cash.
- [ ] Existing position and pyramiding disabled/enabled.
- [ ] Maximum positions reached.
- [ ] Per-position and total exposure exceeded.
- [ ] Concurrent/pending order conflict.
- [ ] Broker rejection and partial fill.
- [ ] Exit with multiple simultaneous triggers.

### Regression verification

- [ ] Existing characterized legacy cases preserve decisions.
- [ ] Full `go test ./...` passes.
- [ ] No runtime mode calls the old execution path except behind an explicit migration flag.

### Cannot yet be proven

- [ ] Historical point-in-time parity remains limited until Stage 04 supplies correct universe/data.
- [ ] Next-bar and lower-timeframe fill realism is completed in Stage 03.
- [ ] New strategy quality is out of scope until Stages 05–07.
- [ ] Real exchange response edge cases require provider fixtures or controlled testnet evidence.

## Acceptance criteria

- [ ] Strategy and risk logic have one implementation across modes.
- [ ] Brokers contain execution differences only.
- [ ] Ledger is the only economic state mutation path.
- [ ] Rollout state, not artifact presence, controls model authority.
- [ ] Reviewer confirms package direction has no handler/database/global-state leakage into pure logic.
